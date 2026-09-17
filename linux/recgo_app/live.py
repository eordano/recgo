from PySide6.QtCore import Qt, QTimer, Signal
from PySide6.QtCore import QUrl
from PySide6.QtGui import QColor, QDesktopServices, QFont, QGuiApplication
from PySide6.QtWidgets import QApplication, QLabel, QScrollArea, QWidget

from recgo_app.model import EventKind, SessionEvent
from recgo_app.recorder import Recorder
from recgo_app.settings import AppSettings, RecordMode
from recgo_app.theme import Theme, css, white
from recgo_app.widgets import Button, Caret, ElidedLabel, hbox, hline, label, set_color, vbox, vline


class EventRow(QWidget):
    def __init__(self, event, size=13, parent=None):
        super().__init__(parent)
        lay = hbox(self, spacing=11)
        lay.setAlignment(Qt.AlignmentFlag.AlignTop)
        stamp = label(event.stamp, 11, QFont.Weight.DemiBold, Theme.faint, mono=True)
        stamp.setFixedWidth(38)
        lay.addWidget(stamp, 0, Qt.AlignmentFlag.AlignTop)
        dot = QWidget()
        dot.setFixedSize(7, 7)
        dot.setStyleSheet("background: %s; border-radius: 3px;" % css(event.kind.color))
        lay.addWidget(dot, 0, Qt.AlignmentFlag.AlignTop)
        lay.addSpacing(0)
        text = label(event.text, size, color=event.kind.text_color, mono=event.kind.mono, wrap=True)
        text.setTextInteractionFlags(Qt.TextInteractionFlag.TextSelectableByMouse)
        lay.addWidget(text, 1)


class LiveWindow(QWidget):
    closed = Signal()

    def __init__(self):
        super().__init__(None, Qt.WindowType.Window)
        self.setWindowTitle("Recgo Live")
        self.setMinimumSize(520, 400)
        self.resize(660, 620)
        self.setStyleSheet("background: %s;" % css(Theme.bg))
        root = hbox(self)
        root.addWidget(self._doc_column(), 1)
        root.addWidget(vline())
        root.addWidget(self._portal_column())
        rec = Recorder.shared()
        rec.live_changed.connect(self.refresh)
        rec.phase_changed.connect(lambda _: self.refresh())
        rec.tick.connect(self._refresh_tail)
        self._rendered = 0
        self._pending = ""
        self.refresh()

    def _doc_column(self):
        col = QWidget()
        lay = vbox(col)
        head = QWidget()
        hl = vbox(head, (16, 14, 16, 10), 4)
        row = hbox(spacing=8)
        self.title = label("Untitled session · titled when you stop", 16, QFont.Weight.Bold)
        row.addWidget(self.title)
        self.rewriting = label("rewritten as you go", 11, color=Theme.muted)
        row.addWidget(self.rewriting)
        row.addStretch()
        hl.addLayout(row)
        self.path = ElidedLabel(AppSettings.shared().out_root, 11, color=Theme.faint, mono=True)
        hl.addWidget(self.path)
        self.page = ElidedLabel("", 11, color=Theme.secondary, mono=True)
        hl.addWidget(self.page)
        self.facts = ElidedLabel("", 11, color=Theme.faint)
        hl.addWidget(self.facts)
        self.data = label("", 11, color=Theme.faint, wrap=True)
        hl.addWidget(self.data)
        lay.addWidget(head)
        lay.addWidget(hline())
        self.scroll = QScrollArea()
        self.scroll.setWidgetResizable(True)
        self.scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarPolicy.ScrollBarAlwaysOff)
        self.list = QWidget()
        self.list_lay = vbox(self.list, (16, 12, 16, 12), 11)
        self.list_lay.setAlignment(Qt.AlignmentFlag.AlignTop)
        tail = QWidget()
        tl = hbox(tail, spacing=11)
        tl.addSpacing(38 + 11)
        d = QWidget()
        d.setFixedSize(7, 7)
        d.setStyleSheet("background: %s; border-radius: 3px;" % css(Theme.faint))
        tl.addWidget(d)
        self.tail_text = label("listening", 13, color=Theme.faint, wrap=True)
        tl.addWidget(self.tail_text, 1)
        tl.addWidget(Caret(), 0, Qt.AlignmentFlag.AlignVCenter)
        self.tail = tail
        self.tail_dot = d
        self.list_lay.addWidget(tail)
        self.scroll.setWidget(self.list)
        lay.addWidget(self.scroll, 1)
        return col

    def _portal_column(self):
        col = QWidget()
        col.setFixedWidth(216)
        col.setStyleSheet("background: rgba(0,0,0,0.22);")
        lay = vbox(col)
        self.portal_on = QWidget()
        pl = vbox(self.portal_on)
        top = QWidget()
        tl = hbox(top, (14, 14, 14, 14), 8)
        dot = QWidget()
        dot.setFixedSize(8, 8)
        dot.setStyleSheet("background: #FFBC5B; border-radius: 4px;")
        tl.addWidget(dot, 0, Qt.AlignmentFlag.AlignTop)
        tc = vbox(spacing=1)
        tc.addWidget(label("Portal room open", 13, QFont.Weight.DemiBold, Theme.amber))
        tc.addWidget(label("Everyone in the room can read every session in the folder above, for as long "
                           "as the recording runs.", 11, color=Theme.faint, wrap=True))
        tl.addLayout(tc, 1)
        pl.addWidget(top)
        pl.addWidget(hline())
        pl.addStretch()
        bottom = QWidget()
        bl = vbox(bottom, (14, 14, 14, 14), 6)
        self.room = label("", 11, QFont.Weight.DemiBold, Theme.amber, mono=True)
        bl.addWidget(self.room)
        open_btn = Button("Open portal room", fill=Theme.amber, hover=QColor("#FFC978"), color=QColor("#1A1508"),
                          size=12, bold=True, height=30, radius=9, expanding=True)
        open_btn.clicked.connect(self._open_room)
        bl.addWidget(open_btn)
        copy = Button("Copy room", size=12, height=30, radius=9, expanding=True)
        copy.clicked.connect(self._copy_room)
        bl.addWidget(copy)
        pl.addWidget(bottom)
        lay.addWidget(self.portal_on)
        self.portal_off = QWidget()
        ol = vbox(self.portal_off, (14, 14, 14, 14))
        ol.addStretch()
        note = label("No portal room. Nothing here is shared while you record; Settings → Data & sharing "
                     "opens a room an agent can follow.", 13, color=Theme.muted, wrap=True)
        note.setAlignment(Qt.AlignmentFlag.AlignCenter)
        note.setStyleSheet("color: %s; background: transparent; border: 1px dashed %s; "
                           "border-radius: 13px; padding: 14px;" % (css(Theme.muted), css(white(0.16))))
        ol.addWidget(note)
        ol.addStretch()
        lay.addWidget(self.portal_off)
        return col

    def _open_room(self):
        rec = Recorder.shared()
        url = AppSettings.shared().portal_page(rec.active_room or AppSettings.shared().portalRoom)
        if url:
            QDesktopServices.openUrl(QUrl(url))

    def _copy_room(self):
        rec = Recorder.shared()
        QGuiApplication.clipboard().setText(rec.active_room or AppSettings.shared().portalURL)

    def refresh(self):
        rec = Recorder.shared()
        s = AppSettings.shared()
        if rec.mode == RecordMode.audio:
            self.title.setText(rec.where_label if rec.is_busy else "Audio recording")
        else:
            self.title.setText("Untitled session · titled when you stop" if rec.is_recording
                               else (rec.live_doc.title or "Session"))
        self.rewriting.setVisible(rec.is_recording and rec.mode != RecordMode.audio)
        self.path.set_text(rec.where or s.out_root)
        page = ""
        if rec.tab_title or rec.tab_url:
            page = " — ".join(p for p in (rec.tab_title, rec.tab_url) if p)
        elif rec.mode == RecordMode.window and rec.source:
            page = rec.source
        elif rec.live_doc.page:
            page = rec.live_doc.page
        self.page.set_text(page)
        self.page.setVisible(bool(page))
        self.facts.set_text(" · ".join(rec.facts))
        self.facts.setVisible(bool(rec.facts))
        lines = rec.data_lines if rec.is_busy else s.data_lines()
        self.data.setText("\n".join(t for t, _ in lines))
        set_color(self.data, Theme.amber if any(l for _, l in lines) else Theme.faint)
        self.portal_on.setVisible(s.portal_active)
        self.portal_off.setVisible(not s.portal_active)
        self.room.setText(rec.active_room or s.portalURL)
        events = rec.live_doc.events
        if len(events) < self._rendered:
            self._clear_rows()
        new_events = events[self._rendered:]
        if self._pending and any(ev.kind == EventKind.narration and (self._pending in ev.text or ev.text in self._pending)
                                 for ev in new_events):
            self._pending = ""
        for ev in new_events:
            self.list_lay.insertWidget(self.list_lay.count() - 1, EventRow(ev))
        self._rendered = len(events)
        QTimer.singleShot(0, lambda: self.scroll.verticalScrollBar().setValue(
            self.scroll.verticalScrollBar().maximum()))

    # Partials are never dropped: an open utterance shows in the tail row, and
    # when the recorder moves on without a final line replacing it (a new
    # utterance began, or the final came back empty) the last partial stays
    # as a dimmed "unconfirmed" row.
    def _refresh_tail(self):
        rec = Recorder.shared()
        hearing = rec.hearing
        if hearing:
            if self.tail_text.text() != hearing:
                if self._pending and not hearing.startswith(self._pending[: max(1, len(self._pending) // 2)]):
                    self._keep_pending()
                self._pending = hearing
                self.tail_text.setText(hearing)
                set_color(self.tail_text, Theme.secondary)
                self.tail_dot.setStyleSheet("background: %s; border-radius: 3px;" % css(Theme.accent))
                QTimer.singleShot(0, lambda: self.scroll.verticalScrollBar().setValue(
                    self.scroll.verticalScrollBar().maximum()))
        elif self.tail_text.text() != "listening":
            self._keep_pending()
            self._pending = ""
            self.tail_text.setText("listening")
            set_color(self.tail_text, Theme.faint)
            self.tail_dot.setStyleSheet("background: %s; border-radius: 3px;" % css(Theme.faint))

    def _keep_pending(self):
        text = self._pending
        if not text:
            return
        for ev in Recorder.shared().live_doc.events[-4:]:
            if ev.kind == EventKind.narration and (text in ev.text or ev.text in text):
                return
        row = EventRow(SessionEvent(Recorder.shared().elapsed, EventKind.narration, text + "  (unconfirmed)"))
        for lb in row.findChildren(QLabel):
            set_color(lb, Theme.muted)
        self.list_lay.insertWidget(self.list_lay.count() - 1, row)

    def _clear_rows(self):
        while self.list_lay.count() > 1:
            item = self.list_lay.takeAt(0)
            if item.widget():
                item.widget().hide()
                item.widget().deleteLater()
        self._rendered = 0

    def reset(self):
        self._clear_rows()
        self.refresh()

    def keyPressEvent(self, e):
        if e.key() == Qt.Key.Key_Escape or (e.key() == Qt.Key.Key_W and e.modifiers() & Qt.KeyboardModifier.ControlModifier):
            self.close()
        else:
            super().keyPressEvent(e)

    def closeEvent(self, e):
        if not QApplication.platformName().startswith("wayland"):
            AppSettings.shared().save_geometry("live", self.saveGeometry())
        super().closeEvent(e)
        self.closed.emit()

    def place(self):
        geo = AppSettings.shared().geometry("live")
        if geo:
            self.restoreGeometry(geo)
            return
        screen = QApplication.primaryScreen()
        if screen is None:
            return
        f = screen.availableGeometry()
        self.move(f.left() + 40, f.top() + 40)
