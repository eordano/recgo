import os
import subprocess

from PySide6.QtCore import QObject, QRectF, QRunnable, QSize, Qt, QThreadPool, QTimer, QUrl, Signal
from PySide6.QtGui import QColor, QFont, QGuiApplication, QImage, QImageReader, QPainter, QPen, QPixmap
from PySide6.QtMultimedia import QAudioOutput, QMediaPlayer
from PySide6.QtWidgets import (QApplication, QFileDialog, QGraphicsDropShadowEffect, QLabel, QLineEdit, QMenu,
                               QScrollArea, QSizePolicy, QStackedWidget, QWidget)

from recgo_app.model import EventKind, clock_string, scan_library, waveform_bins
from recgo_app.settings import AppSettings
from recgo_app.theme import Theme, css, white
from recgo_app.widgets import (Button, ElidedLabel, HoverRow, Segment, hbox, highlight, hline, label, mono_field,
                               open_path, reveal, rich_label, set_color, vbox, vline)


_hay = {}


def haystack(session):
    key = (session.path, session.modified)
    h = _hay.get(key)
    if h is None:
        if len(_hay) > 2000:
            _hay.clear()
        h = (session.doc.title + " " + " ".join(e.text for e in session.doc.events)).lower()
        _hay[key] = h
    return h


class ImageLoader(QObject):
    loaded = Signal(str, int, int, QImage)
    _instance = None

    @classmethod
    def shared(cls):
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        super().__init__()
        self.cache = {}
        self.pending = set()
        self.loaded.connect(self._store)

    def _store(self, path, w, h, img):
        self.pending.discard((path, w, h))
        if len(self.cache) > 300:
            self.cache.clear()
        self.cache[(path, w, h)] = QPixmap.fromImage(img) if not img.isNull() else None

    def get(self, path, w, h):
        key = (path, w, h)
        if key in self.cache:
            return self.cache[key]
        if key not in self.pending:
            self.pending.add(key)
            QThreadPool.globalInstance().start(_ImageJob(self, path, w, h))
        return None


class _ImageJob(QRunnable):
    def __init__(self, loader, path, w, h):
        super().__init__()
        self.loader, self.path, self.w, self.h = loader, path, w, h

    def run(self):
        reader = QImageReader(self.path)
        reader.setAutoTransform(True)
        size = reader.size()
        if size.isValid() and self.w > 0 and self.h > 0:
            scale = max(self.w / max(size.width(), 1), self.h / max(size.height(), 1))
            if scale < 1:
                reader.setScaledSize(QSize(max(1, int(size.width() * scale + 0.5)),
                                           max(1, int(size.height() * scale + 0.5))))
        img = reader.read()
        self.loader.loaded.emit(self.path, self.w, self.h, img if not img.isNull() else QImage())


class LibraryStore(QObject):
    changed = Signal()
    selection_changed = Signal()
    hover_changed = Signal()
    toast_changed = Signal(str)
    preview_changed = Signal()
    _instance = None

    @classmethod
    def shared(cls):
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        super().__init__()
        self.sessions = []
        self.selected_id = None
        self.search = ""
        self.mode_filter = None
        self.reading = False
        self.preview_index = None
        self.hover_t = None
        self.active_root = None

    @property
    def current_root(self):
        return self.active_root or AppSettings.shared().out_root

    @property
    def selected(self):
        return next((s for s in self.sessions if s.id == self.selected_id), None)

    def _matches_mode(self, s, mode):
        return mode in s.doc.tool or (mode == "screen" and s.doc.tool == "recgo-desktop")

    @property
    def filtered(self):
        out = []
        q = self.search.lower().strip()
        for s in self.sessions:
            if self.mode_filter and not self._matches_mode(s, self.mode_filter):
                continue
            if q and q not in haystack(s):
                continue
            out.append(s)
        return out

    def mode_count(self, mode):
        return sum(1 for s in self.sessions if self._matches_mode(s, mode))

    def reload(self, selecting=None):
        self.sessions = scan_library(self.current_root)
        if selecting and self.active_root and not any(s.id == selecting for s in self.sessions):
            self.active_root = None
            self.sessions = scan_library(self.current_root)
        ids = [s.id for s in self.sessions]
        if selecting and selecting in ids:
            self.selected_id = selecting
        elif self.selected_id not in ids:
            self.selected_id = ids[0] if ids else None
        self.preview_index = None
        self.hover_t = None
        self.changed.emit()
        self.selection_changed.emit()

    def select(self, sid):
        if sid != self.selected_id:
            self.selected_id = sid
            self.selection_changed.emit()

    def set_hover(self, t):
        if t != self.hover_t:
            self.hover_t = t
            self.hover_changed.emit()

    def set_preview(self, idx):
        self.preview_index = idx
        self.preview_changed.emit()

    def toast(self, msg):
        self.toast_changed.emit(msg)


class _BinJob(QRunnable):
    def __init__(self, path, done):
        super().__init__()
        self.path = path
        self.done = done

    def run(self):
        self.done(waveform_bins(self.path))


class Player(QObject):
    changed = Signal()
    bins_ready = Signal(object)

    def __init__(self, parent=None):
        super().__init__(parent)
        self.playing = False
        self.t = 0.0
        self.rate = 1.0
        self.clip_in = None
        self.clip_out = None
        self.duration = 0.0
        self.bins = []
        self.has_audio = False
        self.path = None
        self._clip_play = False
        self._out = QAudioOutput(self)
        self._player = QMediaPlayer(self)
        self._player.setAudioOutput(self._out)
        self._player.positionChanged.connect(self._pos)
        self._player.playbackStateChanged.connect(self._state)
        self._player.durationChanged.connect(self._dur)
        self.bins_ready.connect(self._set_bins)

    def load(self, session):
        self._player.stop()
        self.playing = False
        self.t = 0.0
        self.clip_in = self.clip_out = None
        self._clip_play = False
        self.bins = []
        self.duration = float(session.doc.duration_sec)
        wav = session.file("audio.wav")
        self.path = wav
        self.has_audio = os.path.exists(wav)
        if self.has_audio:
            self._player.setSource(QUrl.fromLocalFile(wav))
            path = wav
            QThreadPool.globalInstance().start(
                _BinJob(wav, lambda bins, p=path: self._bins_done(p, bins)))
        else:
            self._player.setSource(QUrl())
        self.changed.emit()

    def _bins_done(self, path, bins):
        if path == self.path:
            self.bins_ready.emit(bins)

    def _set_bins(self, bins):
        self.bins = bins
        self.changed.emit()

    def _dur(self, ms):
        if ms > 0:
            self.duration = max(self.duration, ms / 1000.0)
            self.changed.emit()

    def _pos(self, ms):
        self.t = ms / 1000.0
        if self._clip_play and self.clip_range and self.t >= self.clip_range[1]:
            self._clip_play = False
            self.pause()
            self.seek(self.clip_range[1])
        self.changed.emit()

    def _state(self, state):
        self.playing = state == QMediaPlayer.PlaybackState.PlayingState
        self.changed.emit()

    def toggle(self):
        self.pause() if self.playing else self.play()

    def play(self, from_t=None):
        if not self.has_audio:
            return
        if from_t is not None:
            self._player.setPosition(int(min(from_t, self.duration) * 1000))
        self._player.setPlaybackRate(self.rate)
        self._player.play()

    def pause(self):
        self._player.pause()

    def seek(self, t):
        self.t = t
        self._player.setPosition(int(t * 1000))
        self.changed.emit()

    def cycle_rate(self):
        self.rate = {1.0: 1.5, 1.5: 2.0}.get(self.rate, 1.0)
        self._player.setPlaybackRate(self.rate)
        self.changed.emit()

    @property
    def clip_range(self):
        if self.clip_in is None or self.clip_out is None:
            return None
        return (min(self.clip_in, self.clip_out), max(self.clip_in, self.clip_out))

    def play_clip(self):
        if not self.clip_range:
            return
        self._clip_play = True
        self.play(self.clip_range[0])

    def export_clip(self, session, done):
        if not self.clip_range or not self.path:
            return
        lo, hi = self.clip_range
        clips = session.file("clips")
        os.makedirs(clips, exist_ok=True)
        name = "clip-%s-%s.m4a" % (clock_string(lo).replace(":", ""), clock_string(hi).replace(":", ""))
        out = os.path.join(clips, name)
        try:
            r = subprocess.run(["ffmpeg", "-y", "-loglevel", "error", "-ss", "%.3f" % lo, "-to",
                                "%.3f" % hi, "-i", self.path, "-c:a", "aac", out],
                               capture_output=True, text=True, timeout=120)
        except (OSError, subprocess.TimeoutExpired) as e:
            done("export failed: %s" % e)
            return
        done("Clip written to %s" % out if r.returncode == 0
             else "export failed: %s" % r.stderr.strip()[-200:])


class ShotImage(QLabel):
    def __init__(self, path, w=118, h=70, parent=None):
        super().__init__(parent)
        self.path = path
        self.setFixedSize(w, h)
        self.setStyleSheet("background: qlineargradient(x1:0,y1:0,x2:1,y2:1, stop:0 #2A2731, stop:1 #1B1A1F);"
                           " border-radius: 8px;")
        self.setAlignment(Qt.AlignmentFlag.AlignCenter)
        self._done = False
        ImageLoader.shared().loaded.connect(self._maybe)
        self._try()

    def _maybe(self, path, w, h, img):
        if not self._done and path == self.path and (w, h) == (self.width(), self.height()):
            self._try()

    def _try(self):
        pm = ImageLoader.shared().get(self.path, self.width(), self.height())
        if pm is None:
            return
        self._done = True
        scaled = pm.scaled(self.width(), self.height(), Qt.AspectRatioMode.KeepAspectRatioByExpanding,
                           Qt.TransformationMode.SmoothTransformation)
        self.setPixmap(scaled.copy((scaled.width() - self.width()) // 2, 0, self.width(), self.height()))


class Waveform(QWidget):
    def __init__(self, player, store, parent=None):
        super().__init__(parent)
        self.player = player
        self.store = store
        self.setFixedHeight(46)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        player.changed.connect(self.update)
        store.hover_changed.connect(self.update)

    def mouseReleaseEvent(self, e):
        total = max(self.player.duration, 1)
        sec = e.position().x() / max(self.width(), 1) * total
        if self.player.has_audio:
            self.player.play(sec)
        else:
            self.player.seek(sec)

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        w, h = self.width(), self.height()
        total = max(self.player.duration, 1)
        bins = self.player.bins
        p.setPen(Qt.PenStyle.NoPen)
        if not bins:
            p.setPen(Theme.faint)
            p.drawText(self.rect(), Qt.AlignmentFlag.AlignCenter,
                       "reading waveform…" if self.player.has_audio else "no audio track")
        else:
            n = len(bins)
            bw = (w - 2 * (n - 1)) / n
            for i, v in enumerate(bins):
                sec = (i + 0.5) / n * total
                p.setBrush(Theme.accent if sec <= self.player.t else white(0.2))
                bh = max(3, v * 44)
                p.drawRoundedRect(QRectF(i * (bw + 2), h - bh, bw, bh), 1.5, 1.5)
        if self.player.clip_range:
            lo, hi = self.player.clip_range
            p.setPen(QPen(Theme.accent, 1))
            p.setBrush(QColor(255, 45, 85, 36))
            p.drawRoundedRect(QRectF(lo / total * w, 0, max(4, (hi - lo) / total * w), h), 6, 6)
        if self.store.hover_t is not None:
            p.setPen(Qt.PenStyle.NoPen)
            p.setBrush(QColor(255, 188, 91, 140))
            p.drawRect(QRectF(min(self.store.hover_t / total, 1) * max(w - 2, 0), 0, 1.5, h))
        p.setBrush(QColor("white"))
        p.drawRect(QRectF(min(self.player.t / total, 1) * max(w - 2, 0), 0, 2, h))


class LibraryWindow(QWidget):
    closed = Signal()

    def __init__(self):
        super().__init__(None, Qt.WindowType.Window)
        self.setWindowTitle("Recgo")
        self.setMinimumSize(980, 620)
        self.resize(1180, 760)
        self.setStyleSheet("background: %s;" % css(Theme.bg))
        self.store = LibraryStore.shared()
        self.player = Player(self)
        self.settings = AppSettings.shared()
        self._session_rows = {}
        self._generation = 0
        self._pending = []
        self._reading_labels = []
        root = vbox(self)
        root.addWidget(self._toolbar())
        root.addWidget(hline())
        body = hbox()
        body.addWidget(self._sidebar())
        body.addWidget(vline())
        body.addWidget(self._session_list())
        body.addWidget(vline())
        self.detail_host = QWidget()
        self.detail_lay = vbox(self.detail_host)
        body.addWidget(self.detail_host, 1)
        root.addLayout(body, 1)
        self.toast = label("", 13, QFont.Weight.DemiBold)
        self.toast.setParent(self)
        self.toast.setStyleSheet("color: %s; background: %s; border: 1px solid %s; border-radius: 13px;"
                                 " padding: 11px 18px;" % (css(Theme.text), css(QColor(32, 30, 37, 242)),
                                                           css(white(0.16))))
        self.toast.hide()
        self.preview = ShotPreview(self)
        self.preview.hide()
        self.store.changed.connect(self._rebuild_all)
        self.store.selection_changed.connect(self._on_select)
        self.store.toast_changed.connect(self._show_toast)
        self.store.preview_changed.connect(self._on_preview)
        self.player.changed.connect(self._on_player)
        self.settings.changed.connect(lambda k: self._rebuild_all() if k == "extraRoot" else None)
        self._rebuild_all()
        self._on_select()
        geo = self.settings.geometry("library")
        if geo:
            self.restoreGeometry(geo)

    def closeEvent(self, e):
        self.player.pause()
        self.settings.save_geometry("library", self.saveGeometry())
        super().closeEvent(e)
        self.closed.emit()

    def hideEvent(self, e):
        self.player.pause()
        super().hideEvent(e)

    def resizeEvent(self, e):
        self.preview.setGeometry(self.rect())
        self._place_toast()

    def _place_toast(self):
        self.toast.adjustSize()
        self.toast.move((self.width() - self.toast.width()) // 2, self.height() - self.toast.height() - 20)

    def _show_toast(self, msg):
        self.toast.setText(msg)
        self.toast.show()
        self.toast.raise_()
        self._place_toast()
        QTimer.singleShot(2200, lambda: self.toast.hide() if self.toast.text() == msg else None)

    def _toolbar(self):
        bar = QWidget()
        bar.setFixedHeight(50)
        bar.setStyleSheet("background: rgba(255,255,255,0.03);")
        lay = hbox(bar, (16, 0, 16, 0), 14)
        lay.addWidget(label("Recgo", 14, QFont.Weight.Bold))
        settings_button = Button("Settings…", size=12, height=30, radius=9, color=Theme.secondary)
        settings_button.setToolTip("Open Recgo settings")
        settings_button.clicked.connect(self._open_settings)
        lay.addWidget(settings_button)
        lay.addStretch()
        self.search = QLineEdit()
        self.search.setPlaceholderText("⌕  Search titles and transcripts")
        self.search.setFixedSize(300, 30)
        self.search.setClearButtonEnabled(True)
        self.search.setToolTip("Ctrl+F")
        self._search_timer = QTimer(self)
        self._search_timer.setSingleShot(True)
        self._search_timer.setInterval(220)
        self._search_timer.timeout.connect(self._apply_search)
        self.search.textChanged.connect(lambda _: self._search_timer.start())
        lay.addWidget(self.search)
        self.view_seg = Segment([("timeline", "Timeline"), ("reading", "Reading")], "timeline", radius=7)
        self.view_seg.changed.connect(self._on_view)
        lay.addWidget(self.view_seg)
        self.copy_remote = Button("Copy remote", size=12, height=30, radius=9, color=Theme.secondary)
        self.copy_remote.setToolTip("Copies the rsync destination this session synced to")
        self.copy_remote.clicked.connect(self._copy_remote)
        lay.addWidget(self.copy_remote)
        copy_path = Button("Copy path", fill=Theme.accent, hover=Theme.accent_hover, size=12, bold=True,
                           height=30, radius=9)
        copy_path.clicked.connect(self._copy_path)
        lay.addWidget(copy_path)
        return bar

    def _open_settings(self):
        from recgo_app.windows import Windows
        Windows.shared().show_settings()

    def _copy_remote(self):
        s = self.store.selected
        if s:
            remote = self.settings.remote_location(s.id)
            if remote:
                QGuiApplication.clipboard().setText(remote)
                self.store.toast("Remote location copied")

    def _copy_path(self):
        s = self.store.selected
        if s:
            QGuiApplication.clipboard().setText(s.path)
            self.store.toast("Path copied")

    def _apply_search(self):
        text = self.search.text()
        if text == self.store.search:
            return
        self.store.search = text
        self._rebuild_list()
        for row in getattr(self, "_timeline_rows", []):
            row.set_query(text)
        for lb, raw in getattr(self, "_reading_labels", []):
            lb.setText(highlight(raw, text))

    def _on_view(self, key):
        self.store.reading = key == "reading"
        self._rebuild_detail()

    def _sidebar(self):
        side = QWidget()
        side.setFixedWidth(186)
        self.side_lay = vbox(side, (10, 14, 10, 14), 16)
        return side

    def _side_row(self, text, count, active, cb):
        row = HoverRow(radius=9, base=QColor(255, 45, 85, 41) if active else QColor(0, 0, 0, 0),
                       hover=white(0.07))
        lay = hbox(row, (10, 7, 10, 7))
        lay.addWidget(label(text, 13, QFont.Weight.DemiBold, Theme.text if active else Theme.secondary))
        lay.addStretch()
        if count is not None:
            lay.addWidget(label(str(count), 11, QFont.Weight.DemiBold, Theme.muted if active else Theme.faint))
        row.clicked.connect(cb)
        return row

    def _rebuild_sidebar(self):
        lay = self.side_lay
        while lay.count():
            item = lay.takeAt(0)
            if item.widget():
                item.widget().hide()
                item.widget().deleteLater()
        st = self.store
        lay.addWidget(self._side_row("All Sessions", len(st.sessions), st.mode_filter is None,
                                     lambda: self._set_mode(None)))
        modes = QWidget()
        ml = vbox(modes, spacing=2)
        ml.addWidget(label("Mode", 12, QFont.Weight.DemiBold, Theme.faint))
        for m in ("screen", "window", "browser", "tab", "audio"):
            ml.addWidget(self._side_row(m.capitalize(), st.mode_count(m), st.mode_filter == m,
                                        lambda m=m: self._set_mode(None if st.mode_filter == m else m)))
        lay.addWidget(modes)
        sources = QWidget()
        sl = vbox(sources, spacing=2)
        sl.addWidget(label("Sources", 12, QFont.Weight.DemiBold, Theme.faint))
        sl.addWidget(self._side_row("This computer", None, st.active_root is None, lambda: self._set_root(None)))
        extra = self.settings.extraRoot
        if extra:
            path = os.path.expanduser(extra)
            r = self._side_row(os.path.basename(path.rstrip("/")) or path, None, st.active_root == path,
                               lambda p=path: self._set_root(p))
            r.setToolTip(path)
            sl.addWidget(r)
        sl.addWidget(self._side_row("Browse folder…", None, False, self._browse))
        lay.addWidget(sources)
        lay.addStretch()
        b = Button("Reveal folder…", size=12, fill=QColor(0, 0, 0, 0), color=Theme.secondary, height=28,
                   radius=8)
        b.clicked.connect(lambda: open_path(st.current_root))
        lay.addWidget(b, 0, Qt.AlignmentFlag.AlignLeft)

    def _set_mode(self, m):
        self.store.mode_filter = m
        self._rebuild_sidebar()
        self._rebuild_list()

    def _set_root(self, root):
        self.store.active_root = root
        self.store.reload()

    def _browse(self):
        d = QFileDialog.getExistingDirectory(self, "Pick a folder of recgo sessions — a mounted remote "
                                             "volume works too", self.store.current_root)
        if d:
            self.settings.extraRoot = d
            self._set_root(d)

    def _session_list(self):
        self.list_scroll = QScrollArea()
        self.list_scroll.setFixedWidth(274)
        self.list_scroll.setWidgetResizable(True)
        self.list_scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        self.list_host = QWidget()
        self.list_lay = vbox(self.list_host, (8, 8, 8, 8), 3)
        self.list_lay.setAlignment(Qt.AlignmentFlag.AlignTop)
        self.list_scroll.setWidget(self.list_host)
        return self.list_scroll

    def _rebuild_list(self):
        lay = self.list_lay
        while lay.count():
            item = lay.takeAt(0)
            if item.widget():
                item.widget().hide()
                item.widget().deleteLater()
        self._session_rows = {}
        for s in self.store.filtered:
            active = s.id == self.store.selected_id
            row = HoverRow(base=QColor(255, 45, 85, 41) if active else QColor(0, 0, 0, 0), hover=white(0.05))
            rl = vbox(row, (12, 11, 12, 11), 5)
            title = rich_label(highlight(s.doc.title or s.id, self.store.search), 13,
                               Theme.text if active else Theme.secondary, weight=QFont.Weight.DemiBold)
            title.setTextInteractionFlags(Qt.TextInteractionFlag.NoTextInteraction)
            rl.addWidget(title)
            meta = label(s.meta, 11, QFont.Weight.DemiBold, Theme.faint)
            rl.addWidget(meta)
            row.clicked.connect(lambda sid=s.id: self.store.select(sid))
            row.setContextMenuPolicy(Qt.ContextMenuPolicy.CustomContextMenu)
            row.customContextMenuRequested.connect(lambda pos, s=s, row=row: self._session_menu(s, row.mapToGlobal(pos)))
            lay.addWidget(row)
            self._session_rows[s.id] = row
        if not self.store.filtered:
            empty = label("No matches" if self.store.search or self.store.mode_filter else "No sessions yet",
                          12, QFont.Weight.DemiBold, Theme.faint)
            empty.setAlignment(Qt.AlignmentFlag.AlignCenter)
            empty.setContentsMargins(0, 24, 0, 0)
            lay.addWidget(empty)

    def _session_menu(self, s, pos):
        self.store.select(s.id)
        menu = QMenu(self)
        menu.addAction("Open folder", lambda: open_path(s.path))
        menu.addAction("Show in file manager", lambda: reveal(s.path))
        menu.addAction("Copy path", lambda: (QGuiApplication.clipboard().setText(s.path), self.store.toast("Path copied")))
        if self.settings.sync_active:
            menu.addAction("Copy remote location", self._copy_remote)
        if self.settings.session_url(s.id):
            menu.addAction("Copy session URL", lambda: (QGuiApplication.clipboard().setText(
                self.settings.session_url(s.id)), self.store.toast("Session URL copied")))
        menu.addSeparator()
        menu.addAction("Open SESSION.md", lambda: open_path(s.file("SESSION.md")))
        menu.exec(pos)

    def _move_selection(self, delta):
        ids = [s.id for s in self.store.filtered]
        if not ids:
            return
        try:
            i = ids.index(self.store.selected_id)
        except ValueError:
            i = 0 if delta > 0 else len(ids) - 1
            self.store.select(ids[i])
            return
        j = max(0, min(len(ids) - 1, i + delta))
        self.store.select(ids[j])
        row = self._session_rows.get(ids[j])
        if row:
            self.list_scroll.ensureWidgetVisible(row, 0, 24)

    def _on_select(self):
        for sid, row in self._session_rows.items():
            row.set_base(QColor(255, 45, 85, 41) if sid == self.store.selected_id else QColor(0, 0, 0, 0))
        s = self.store.selected
        if s:
            self.player.load(s)
        self._rebuild_detail()

    def _rebuild_all(self):
        self.copy_remote.setVisible(self.settings.sync_active)
        self._rebuild_sidebar()
        self._rebuild_list()

    def _clear_detail(self):
        self._generation += 1
        self._pending = []
        self._reading_labels = []
        while self.detail_lay.count():
            item = self.detail_lay.takeAt(0)
            if item.widget():
                item.widget().hide()
                item.widget().deleteLater()
        self._timeline_rows = []

    def _rebuild_detail(self):
        self._clear_detail()
        s = self.store.selected
        lay = self.detail_lay
        if s is None:
            lay.addStretch()
            t = label("No sessions yet", 15, QFont.Weight.DemiBold, Theme.muted)
            t.setAlignment(Qt.AlignmentFlag.AlignCenter)
            lay.addWidget(t)
            u = label("Start one from the tray icon, or record the screen right now.", 13, color=Theme.faint)
            u.setAlignment(Qt.AlignmentFlag.AlignCenter)
            lay.addWidget(u)
            lay.addSpacing(14)
            from recgo_app.settings import RecordMode
            from recgo_app.tray import Actions
            b = Button("Record screen", fill=Theme.accent, hover=Theme.accent_hover, bold=True, height=34)
            b.clicked.connect(lambda: Actions.start(RecordMode.screen))
            lay.addWidget(b, 0, Qt.AlignmentFlag.AlignHCenter)
            lay.addStretch()
            return
        head = QWidget()
        hl = hbox(head, (22, 16, 22, 12), 12)
        col = vbox(spacing=5)
        col.addWidget(ElidedLabel(s.doc.title or s.id, 20, QFont.Weight.Bold))
        col.addWidget(ElidedLabel(s.path, 12, QFont.Weight.DemiBold, Theme.faint, mono=True))
        hl.addLayout(col, 1)
        b = Button("Open folder", size=12, height=28, radius=8, color=Theme.secondary)
        b.clicked.connect(lambda: open_path(s.path))
        hl.addWidget(b, 0, Qt.AlignmentFlag.AlignTop)
        lay.addWidget(head)
        lay.addWidget(hline())
        if self.store.reading:
            lay.addWidget(self._reading_view(s), 1)
        else:
            lay.addWidget(self._timeline_view(s), 1)

    def _timeline_view(self, s):
        wrap = QWidget()
        wl = vbox(wrap)
        top = QWidget()
        top.setObjectName("timelineTop")
        top.setStyleSheet("#timelineTop { background: %s; }" % css(Theme.list_bg))
        tl = vbox(top)
        if s.doc.shots:
            self.strip = ShotStrip(s, self.player, self.store)
            tl.addWidget(self.strip)
            tl.addWidget(hline())
        tl.addWidget(PlaybackBar(s, self.player, self.store))
        top.setSizePolicy(QSizePolicy.Policy.Preferred, QSizePolicy.Policy.Maximum)
        wl.addWidget(top)
        wl.addWidget(hline())
        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        host = QWidget()
        lay = vbox(host)
        lay.setAlignment(Qt.AlignmentFlag.AlignTop)
        rows = QWidget()
        rows.setSizePolicy(QSizePolicy.Policy.Preferred, QSizePolicy.Policy.Maximum)
        rl = vbox(rows, (22, 14, 22, 28))
        self._timeline_rows = []
        self._pending = list(s.doc.events)
        self._pending_layout = rl
        self._pending_session = s
        self._generation += 1
        self._build_chunk(self._generation, 40)
        lay.addWidget(rows)
        lay.addStretch(1)
        scroll.setWidget(host)
        wl.addWidget(scroll, 1)
        return wrap

    def _build_chunk(self, generation, n):
        if generation != self._generation or not self._pending:
            return
        s = self._pending_session
        for ev in self._pending[:n]:
            row = TimelineRow(s, ev, self)
            self._pending_layout.addWidget(row)
            self._timeline_rows.append(row)
        del self._pending[:n]
        if self._pending:
            QTimer.singleShot(0, lambda: self._build_chunk(generation, 80))
        else:
            self._on_player()

    def _on_player(self):
        s = self.store.selected
        if not s or not getattr(self, "_timeline_rows", None):
            return
        t = self.player.t
        current = None
        for ev in s.doc.events:
            if ev.t <= t:
                current = ev.id
        clip = self.player.clip_range
        for row in self._timeline_rows:
            row.set_state(row.event.id == current,
                          clip is None or clip[0] <= row.event.t <= clip[1])

    def _reading_view(self, s):
        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        host = QWidget()
        outer = hbox(host, (40, 28, 40, 28))
        outer.addStretch()
        col = QWidget()
        col.setMaximumWidth(640)
        col.setSizePolicy(QSizePolicy.Policy.Expanding, QSizePolicy.Policy.Preferred)
        lay = vbox(col, spacing=18)
        lay.setAlignment(Qt.AlignmentFlag.AlignTop)
        lay.addWidget(label(s.doc.title or s.id, 26, QFont.Weight.Bold, wrap=True))
        meta = []
        if s.doc.capture:
            meta.append("capture: " + s.doc.capture)
        if s.doc.host:
            meta.append("host: " + s.doc.host)
        if s.doc.displays:
            meta.append("displays: " + s.doc.displays)
        meta.append("started %s · %s · %d shots" % (s.doc.start, s.duration_label, len(s.doc.shots)))
        lay.addWidget(label("\n".join(meta), 13, color=Theme.faint, mono=True, wrap=True))
        lay.addWidget(hline())
        self._reading_pending = list(s.doc.events)
        self._reading_layout = lay
        self._reading_session = s
        self._generation += 1
        self._reading_chunk(self._generation, 40)
        outer.addWidget(col, 1)
        outer.addStretch()
        scroll.setWidget(host)
        return scroll

    def _reading_chunk(self, generation, n):
        if generation != self._generation or not self._reading_pending:
            return
        for ev in self._reading_pending[:n]:
            self._reading_layout.addWidget(self._reading_row(self._reading_session, ev))
        del self._reading_pending[:n]
        if self._reading_pending:
            QTimer.singleShot(0, lambda: self._reading_chunk(generation, 80))

    def _reading_row(self, s, ev):
        w = HoverRow(radius=6, hover=white(0.04) if ev.img else QColor(0, 0, 0, 0))
        lay = vbox(w, (6, 4, 6, 4), 4)
        head = hbox(spacing=6)
        head.addWidget(label("[%s] %s" % (ev.stamp, ev.kind.value), 11, QFont.Weight.DemiBold, Theme.faint,
                             mono=True))
        if ev.img:
            head.addWidget(label("▣ " + ev.img, 11, color=Theme.faint, mono=True))
        head.addStretch()
        lay.addLayout(head)
        body = rich_label(highlight(ev.text, self.store.search), 15, ev.kind.text_color, ev.kind.mono)
        self._reading_labels.append((body, ev.text))
        lay.addWidget(body)
        if ev.img:
            w.setToolTip("Open %s" % ev.img)
            idx = next((i for i, sh in enumerate(s.doc.shots) if sh.id == ev.id), None)
            w.clicked.connect(lambda i=idx: self.store.set_preview(i))
        else:
            w.setCursor(Qt.CursorShape.ArrowCursor)
        return w

    def _on_preview(self):
        s = self.store.selected
        if self.store.preview_index is None or s is None:
            self.preview.hide()
            return
        self.preview.show_shot(s, self.store.preview_index)
        self.preview.setGeometry(self.rect())
        self.preview.show()
        self.preview.raise_()

    def keyPressEvent(self, e):
        key = e.key()
        mods = e.modifiers()
        ctrl = bool(mods & Qt.KeyboardModifier.ControlModifier)
        typing = self.search.hasFocus()
        if ctrl and key == Qt.Key.Key_F:
            self.search.setFocus()
            self.search.selectAll()
        elif ctrl and key == Qt.Key.Key_W:
            self.close()
        elif key == Qt.Key.Key_Escape:
            if self.preview.isVisible():
                self.store.set_preview(None)
            elif typing:
                self.search.clear()
                self.setFocus()
            else:
                self.close()
        elif self.preview.isVisible() and key in (Qt.Key.Key_Left, Qt.Key.Key_Right):
            self.preview._nav(-1 if key == Qt.Key.Key_Left else 1)
        elif not typing and key == Qt.Key.Key_Space and self.player.has_audio:
            self.player.toggle()
        elif not typing and key in (Qt.Key.Key_Down, Qt.Key.Key_J):
            self._move_selection(1)
        elif not typing and key in (Qt.Key.Key_Up, Qt.Key.Key_K):
            self._move_selection(-1)
        elif not typing and key == Qt.Key.Key_Return and self.store.selected:
            open_path(self.store.selected.path)
        else:
            super().keyPressEvent(e)


class TimelineRow(HoverRow):
    def __init__(self, session, event, lib):
        super().__init__(radius=9, hover=white(0.05))
        self.event = event
        self.session = session
        self.lib = lib
        lay = hbox(self, (9, 7, 9, 7), 12)
        lay.setAlignment(Qt.AlignmentFlag.AlignTop)
        self.stamp = label(event.stamp, 11, QFont.Weight.DemiBold, Theme.faint, mono=True)
        self.stamp.setFixedWidth(40)
        lay.addWidget(self.stamp, 0, Qt.AlignmentFlag.AlignTop)
        dot = QWidget()
        dot.setFixedSize(8, 8)
        dot.setStyleSheet("background: %s; border-radius: 4px;" % css(event.kind.color))
        lay.addWidget(dot, 0, Qt.AlignmentFlag.AlignTop)
        text = rich_label(highlight(event.text, lib.store.search), 14, event.kind.text_color, event.kind.mono)
        text.setTextInteractionFlags(Qt.TextInteractionFlag.NoTextInteraction)
        self.text = text
        lay.addWidget(text, 1)
        if event.img:
            lay.addWidget(label("▣", 11, color=Theme.faint), 0, Qt.AlignmentFlag.AlignTop)
        self.clicked.connect(self._activate)
        self._opacity = 1.0

    def enterEvent(self, e):
        super().enterEvent(e)
        self.lib.store.set_hover(self.event.t)

    def leaveEvent(self, e):
        super().leaveEvent(e)
        self.lib.store.set_hover(None)

    def _activate(self):
        ev = self.event
        player = self.lib.player
        if ev.img:
            idx = next((i for i, sh in enumerate(self.session.doc.shots) if sh.id == ev.id), None)
            player.pause()
            player.seek(ev.t)
            self.lib.store.set_preview(idx)
        elif player.has_audio:
            player.play(ev.t)
        else:
            player.seek(ev.t)

    def set_query(self, q):
        self.text.setText(highlight(self.event.text, q))

    def set_state(self, active, in_clip):
        self.set_base(QColor(255, 45, 85, 31) if active else QColor(0, 0, 0, 0))
        set_color(self.stamp, Theme.pink if active else Theme.faint)
        effect = self.graphicsEffect()
        from PySide6.QtWidgets import QGraphicsOpacityEffect
        if not in_clip:
            if effect is None:
                effect = QGraphicsOpacityEffect(self)
                self.setGraphicsEffect(effect)
            effect.setOpacity(0.3)
        elif effect is not None:
            effect.setOpacity(1.0)


class ShotStrip(QScrollArea):
    def __init__(self, session, player, store, parent=None):
        super().__init__(parent)
        self.session = session
        self.player = player
        self.store = store
        self.setWidgetResizable(True)
        self.setFixedHeight(112)
        self.setVerticalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        self.setStyleSheet("QScrollArea { background: transparent; border: none; }")
        host = QWidget()
        host.setStyleSheet("background: transparent;")
        lay = hbox(host, (22, 10, 22, 10), 8)
        self.thumbs = []
        for i, ev in enumerate(session.doc.shots):
            th = self._thumb(i, ev)
            lay.addWidget(th)
            self.thumbs.append((ev, th))
        lay.addStretch()
        self.setWidget(host)
        store.hover_changed.connect(self._drift)
        player.changed.connect(self._clip_opacity)

    def _thumb(self, i, ev):
        w = QWidget()
        w.setCursor(Qt.CursorShape.PointingHandCursor)
        lay = vbox(w, spacing=5)
        frame = QWidget()
        frame.setFixedSize(118, 70)
        fl = vbox(frame)
        img = ShotImage(self.session.file(ev.img))
        fl.addWidget(img)
        stamp = label(ev.stamp, 9, QFont.Weight.Bold)
        stamp.setParent(frame)
        stamp.setStyleSheet("color: %s; background: rgba(0,0,0,0.62); border-radius: 4px; padding: 1px 5px;"
                            % css(Theme.text))
        stamp.adjustSize()
        stamp.move(5, 70 - stamp.height() - 5)
        frame.setStyleSheet("border-radius: 8px;")
        lay.addWidget(frame)
        kind = ev.kind
        text = {EventKind.click: "click · " + ev.text[:28], EventKind.mark: "mark",
                EventKind.focus: "focus · " + ev.text[:24],
                EventKind.window: "window · " + ev.text[:22]}.get(kind, ev.text)
        cap = label(text, 11, QFont.Weight.DemiBold, Theme.pink if kind == EventKind.click else Theme.faint)
        cap.setFixedWidth(118)
        lay.addWidget(cap)
        w.mouseReleaseEvent = lambda e, i=i, ev=ev: self._open(i, ev)
        w.frame = frame
        return w

    def _open(self, i, ev):
        self.player.pause()
        self.player.seek(ev.t)
        self.store.set_preview(i)

    def _drift(self):
        t = self.store.hover_t
        if t is None or not self.thumbs:
            for ev, th in self.thumbs:
                th.frame.setStyleSheet("border-radius: 8px;")
            return
        ev, th = min(self.thumbs, key=lambda p: abs(p[0].t - t))
        for e2, t2 in self.thumbs:
            t2.frame.setStyleSheet("border: %s; border-radius: 8px;"
                                   % ("1.5px solid rgba(255,188,91,0.7)" if t2 is th else "none"))
        self.ensureWidgetVisible(th, 60, 0)

    def _clip_opacity(self):
        clip = self.player.clip_range
        from PySide6.QtWidgets import QGraphicsOpacityEffect
        for ev, th in self.thumbs:
            inside = clip is None or clip[0] <= ev.t <= clip[1]
            eff = th.graphicsEffect()
            if not inside:
                if eff is None:
                    eff = QGraphicsOpacityEffect(th)
                    th.setGraphicsEffect(eff)
                eff.setOpacity(0.32)
            elif eff is not None:
                eff.setOpacity(1.0)


class PlaybackBar(QWidget):
    def __init__(self, session, player, store, parent=None):
        super().__init__(parent)
        self.session = session
        self.player = player
        self.store = store
        self.setStyleSheet("background: rgba(0,0,0,0.2);")
        lay = vbox(self, (22, 12, 22, 14), 10)
        row = hbox(spacing=12)
        self.play_btn = Button("▶", fill=Theme.accent, hover=Theme.accent_hover, size=13, bold=True,
                               radius=17, height=34)
        self.play_btn.setFixedWidth(34)
        self.play_btn.clicked.connect(player.toggle)
        row.addWidget(self.play_btn)
        self.clock = label("00:00 / 00:00", 13, QFont.Weight.Bold)
        self.clock.setFixedWidth(110)
        row.addWidget(self.clock)
        self.rate_btn = Button("1×", size=12, height=26, radius=8, color=Theme.secondary)
        self.rate_btn.clicked.connect(player.cycle_rate)
        row.addWidget(self.rate_btn)
        row.addStretch()
        b_in = Button("Set start", size=12, height=26, radius=8, color=Theme.secondary)
        b_in.clicked.connect(self._set_start)
        b_out = Button("Set end", size=12, height=26, radius=8, color=Theme.secondary)
        b_out.clicked.connect(self._set_end)
        row.addWidget(b_in)
        row.addWidget(b_out)
        lay.addLayout(row)
        lay.addWidget(Waveform(player, store))
        foot = hbox(spacing=8)
        self.hint = label("", 12, QFont.Weight.DemiBold, Theme.faint)
        foot.addWidget(self.hint)
        foot.addStretch()
        self.b_play_clip = Button("Play clip", size=12, height=26, radius=8, color=Theme.secondary)
        self.b_play_clip.clicked.connect(player.play_clip)
        self.b_export = Button("Export clip", fill=Theme.accent, hover=Theme.accent_hover, size=12, bold=True,
                               height=26, radius=8)
        self.b_export.clicked.connect(lambda: player.export_clip(session, store.toast))
        self.b_clear = Button("Clear", size=12, height=26, radius=8, color=Theme.secondary)
        self.b_clear.clicked.connect(self._clear)
        for b in (self.b_play_clip, self.b_export, self.b_clear):
            foot.addWidget(b)
        lay.addLayout(foot)
        player.changed.connect(self.refresh)
        self.refresh()

    def _set_start(self):
        self.player.clip_in = self.player.t
        self.player.clip_out = None
        self.player.changed.emit()

    def _set_end(self):
        if self.player.clip_in is None:
            self.player.clip_in = 0.0
        self.player.clip_out = self.player.t
        self.player.changed.emit()

    def _clear(self):
        self.player.clip_in = self.player.clip_out = None
        self.player.changed.emit()

    def refresh(self):
        p = self.player
        self.play_btn.setText("❚❚" if p.playing else "▶")
        self.play_btn.setEnabled(p.has_audio)
        self.play_btn.fill = Theme.accent if p.has_audio else Theme.faint
        self.clock.setText("%s / %s" % (clock_string(p.t), clock_string(p.duration)))
        self.rate_btn.setText({1.0: "1×", 1.5: "1.5×"}.get(p.rate, "2×"))
        clip = p.clip_range
        if clip:
            lo, hi = clip
            self.hint.setText("Clip %s – %s · %s long" % (clock_string(lo), clock_string(hi), clock_string(hi - lo)))
            set_color(self.hint, Theme.text)
        elif p.clip_in is not None:
            self.hint.setText("Start at %s — now set the end" % clock_string(p.clip_in))
            set_color(self.hint, Theme.amber)
        else:
            self.hint.setText("Play back, then Set start / Set end to slice a clip")
            set_color(self.hint, Theme.faint)
        for b in (self.b_play_clip, self.b_export, self.b_clear):
            b.setVisible(clip is not None)


class ShotPreview(QWidget):
    def __init__(self, parent):
        super().__init__(parent)
        self.setAutoFillBackground(False)
        self.setStyleSheet("background: rgba(14,13,16,0.97);")
        self.session = None
        self.index = 0
        outer = vbox(self, (24, 24, 24, 24))
        outer.addStretch()
        box = QWidget()
        box.setMaximumWidth(860)
        box.setStyleSheet("background: transparent;")
        lay = vbox(box, spacing=12)
        head = hbox(spacing=12)
        self.kind = label("", 17, QFont.Weight.Bold)
        self.stamp = label("", 13, QFont.Weight.DemiBold, Theme.faint, mono=True)
        self.count = label("", 12, QFont.Weight.DemiBold, Theme.faint)
        head.addWidget(self.kind)
        head.addWidget(self.stamp)
        head.addStretch()
        head.addWidget(self.count)
        for text, cb in (("‹", lambda: self._nav(-1)), ("›", lambda: self._nav(1)),
                         ("✕", lambda: LibraryStore.shared().set_preview(None))):
            b = Button(text, size=13, height=30, radius=9, color=Theme.secondary)
            b.setFixedWidth(30)
            b.clicked.connect(cb)
            head.addWidget(b)
        lay.addLayout(head)
        self.image = QLabel()
        self.image.setAlignment(Qt.AlignmentFlag.AlignCenter)
        self.image.setStyleSheet("border: 1px solid rgba(255,255,255,0.14); border-radius: 12px;"
                                 " background: #0A090B;")
        shadow = QGraphicsDropShadowEffect(self.image)
        shadow.setBlurRadius(60)
        shadow.setOffset(0, 18)
        shadow.setColor(QColor(0, 0, 0, 200))
        self.image.setGraphicsEffect(shadow)
        lay.addWidget(self.image, 1)
        self._want = None
        ImageLoader.shared().loaded.connect(self._maybe)
        foot = hbox(spacing=10)
        self.path = label("", 12, color=Theme.muted, mono=True)
        foot.addWidget(self.path, 1)
        b_copy = Button("Copy image", size=12, height=30, radius=10, color=Theme.secondary)
        b_copy.clicked.connect(self._copy)
        b_path = Button("Copy image path", size=12, height=30, radius=10, color=Theme.secondary)
        b_path.clicked.connect(lambda: self._copy_text(self._current_path(), "Image path copied"))
        self.b_url = Button("Copy image URL", size=12, height=30, radius=10, color=Theme.secondary)
        self.b_url.clicked.connect(self._copy_url)
        b_reveal = Button("Show in file manager", size=12, height=30, radius=10, color=Theme.secondary)
        b_reveal.clicked.connect(lambda: reveal(self._current_path()))
        for b in (b_copy, b_path, self.b_url, b_reveal):
            foot.addWidget(b)
        lay.addLayout(foot)
        hb = hbox()
        hb.addStretch()
        hb.addWidget(box, 1)
        hb.addStretch()
        outer.addLayout(hb, 1)
        outer.addStretch()

    def mouseReleaseEvent(self, e):
        if not self.childAt(e.position().toPoint()):
            LibraryStore.shared().set_preview(None)

    def _shots(self):
        return self.session.doc.shots if self.session else []

    def _current_path(self):
        shots = self._shots()
        if not shots:
            return ""
        return self.session.file(shots[self.index].img)

    def _nav(self, d):
        n = len(self._shots())
        if n:
            LibraryStore.shared().set_preview((self.index + d) % n)

    def _copy_text(self, text, toast):
        if text:
            QGuiApplication.clipboard().setText(text)
            LibraryStore.shared().toast(toast)

    def _copy_url(self):
        shots = self._shots()
        if not shots:
            return
        url = AppSettings.shared().session_url(self.session.id, shots[self.index].img)
        self._copy_text(url, "Image URL copied")

    def _copy(self):
        img = QImage(self._current_path())
        if not img.isNull():
            QGuiApplication.clipboard().setImage(img)
            LibraryStore.shared().toast("Image copied")

    def show_shot(self, session, index):
        self.session = session
        self.index = index
        shots = self._shots()
        if not shots or index >= len(shots):
            return
        shot = shots[index]
        self.b_url.setVisible(bool(AppSettings.shared().get("syncURL")))
        self.kind.setText(shot.kind.value)
        self.stamp.setText(shot.stamp)
        self.count.setText("%d of %d" % (index + 1, len(shots)))
        path = self._current_path()
        self.path.setText(path)
        avail = self.parent().size()
        w, h = min(812, avail.width() - 96), max(200, avail.height() - 200)
        self._want = (path, w, h)
        self.image.setText("")
        self._try()

    def _maybe(self, path, w, h, img):
        if self._want == (path, w, h):
            self._try()

    def _try(self):
        if not self._want:
            return
        path, w, h = self._want
        pm = ImageLoader.shared().get(path, w, h)
        if pm is None:
            if (path, w, h) in ImageLoader.shared().cache:
                self.image.setText("missing " + os.path.basename(path))
            return
        self.image.setPixmap(pm.scaled(w, h, Qt.AspectRatioMode.KeepAspectRatio,
                                       Qt.TransformationMode.SmoothTransformation))


_ = QApplication
