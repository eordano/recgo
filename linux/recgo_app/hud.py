from PySide6.QtCore import QRectF, Qt
from PySide6.QtGui import QColor, QFont, QPainter, QPen
from PySide6.QtWidgets import QApplication, QStackedLayout, QWidget

from recgo_app.model import clock_string
from recgo_app.recorder import Phase, Recorder
from recgo_app.settings import AppSettings, RecordMode, data_summary
from recgo_app.theme import Theme, white
from recgo_app.widgets import Button, Caret, ElidedLabel, RecDot, hbox, label, set_color, vbox

HUD_WIDTH = 380


class Surface(QWidget):
    def __init__(self, radius=14, fill=None, parent=None):
        super().__init__(parent)
        self.radius = radius
        self.fill = fill or QColor(Theme.surface)
        self.fill.setAlphaF(0.96)

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        p.setPen(QPen(white(0.10), 1))
        p.setBrush(self.fill)
        p.drawRoundedRect(QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5), self.radius, self.radius)


class HUDWindow(QWidget):
    """The recording HUD, kept sober: a line of status, the narration as it is
    heard, one line saying where the bytes go (amber only when they leave this
    computer), Mark and Stop. Every value on it is real; nothing decorative."""

    def __init__(self, windows):
        super().__init__(None, Qt.WindowType.Tool | Qt.WindowType.FramelessWindowHint
                         | Qt.WindowType.WindowStaysOnTopHint | Qt.WindowType.WindowDoesNotAcceptFocus)
        self.windows = windows
        self.setWindowTitle("Recgo HUD")
        self.setAttribute(Qt.WidgetAttribute.WA_TranslucentBackground)
        self.setAttribute(Qt.WidgetAttribute.WA_ShowWithoutActivating)
        self.setFocusPolicy(Qt.FocusPolicy.NoFocus)
        self.stack = QStackedLayout(self)
        self.stack.setContentsMargins(2, 2, 2, 2)
        self.stack.setStackingMode(QStackedLayout.StackingMode.StackOne)
        self.panel = self._build_panel()
        self.pill = self._build_pill()
        self.stack.addWidget(self.panel)
        self.stack.addWidget(self.pill)
        self.stack.setCurrentWidget(self.panel)
        self.collapsed = False
        rec = Recorder.shared()
        rec.tick.connect(self.refresh)
        rec.phase_changed.connect(lambda _: self.refresh())
        rec.live_changed.connect(self.refresh)
        self.refresh()

    def mousePressEvent(self, e):
        if e.button() == Qt.MouseButton.LeftButton and self.windowHandle():
            self.windowHandle().startSystemMove()

    def moveEvent(self, e):
        super().moveEvent(e)
        if self.isVisible() and not QApplication.platformName().startswith("wayland"):
            AppSettings.shared().save_geometry("hud", "%d,%d" % (self.x(), self.y()))

    def _build_pill(self):
        w = QWidget()
        outer = hbox(w)
        outer.addStretch()
        surface = Surface(radius=16)
        lay = hbox(surface, (13, 8, 13, 8), 8)
        self.pill_dot = RecDot()
        self.pill_text = label("00:00", 13, QFont.Weight.DemiBold)
        lay.addWidget(self.pill_dot)
        lay.addWidget(self.pill_text)
        surface.setCursor(Qt.CursorShape.PointingHandCursor)
        surface.mouseReleaseEvent = lambda e: self.set_collapsed(False)
        outer.addWidget(surface, 0, Qt.AlignmentFlag.AlignBottom)
        return w

    def _text_button(self, text, tip, slot):
        b = Button(text, size=11, fill=QColor(0, 0, 0, 0), color=Theme.faint, height=18, radius=4)
        b.setFixedWidth(b.sizeHint().width())
        b.setToolTip(tip)
        b.clicked.connect(slot)
        return b

    def _build_panel(self):
        surface = Surface()
        surface.setFixedWidth(HUD_WIDTH)
        lay = vbox(surface, (16, 10, 16, 14), 8)

        head = hbox(spacing=10)
        self.dot = RecDot()
        head.addWidget(self.dot)
        self.elapsed = label("00:00", 18, QFont.Weight.Bold)
        head.addWidget(self.elapsed)
        self.state = label("", 12, QFont.Weight.DemiBold, Theme.secondary)
        head.addWidget(self.state)
        head.addStretch()
        head.addWidget(self._text_button("live", "Open the live session window", lambda: self.windows.show_live()))
        head.addWidget(self._text_button("hide", "Collapse the HUD to its clock", lambda: self.set_collapsed(True)))
        lay.addLayout(head)

        self.source = ElidedLabel("", 12, color=Theme.muted)
        lay.addWidget(self.source)
        self.facts = ElidedLabel("", 11, color=Theme.faint)
        lay.addWidget(self.facts)

        lay.addSpacing(2)
        narration = hbox(spacing=6)
        self.narration = label("waiting for speech…", 14, color=Theme.muted, wrap=True)
        self.narration.setMinimumHeight(40)
        self.narration.setMaximumHeight(60)
        narration.addWidget(self.narration, 1)
        narration.addWidget(Caret(), 0, Qt.AlignmentFlag.AlignVCenter)
        lay.addLayout(narration)

        self.finishing = label("", 12, color=Theme.muted, wrap=True)
        lay.addWidget(self.finishing)

        data = hbox(spacing=8)
        self.data = ElidedLabel("", 11, color=Theme.faint)
        data.addWidget(self.data, 1)
        self.b_room = self._text_button("open room", "Open the portal room in the browser", self._open_room)
        data.addWidget(self.b_room)
        self.b_sys = self._text_button("mute system audio", "", lambda: Recorder.shared().toggle_system_audio())
        data.addWidget(self.b_sys)
        lay.addLayout(data)

        self.buttons = QWidget()
        br = hbox(self.buttons, spacing=8)
        self.b_mark = Button("Mark", height=32, radius=9, expanding=True)
        self.b_mark.setToolTip("Flag this moment in the session document, with a screenshot")
        self.b_mark.clicked.connect(lambda: Recorder.shared().mark())
        self.b_stop = Button("Stop", fill=Theme.accent, hover=Theme.accent_hover, bold=True, height=32, radius=9,
                             expanding=True)
        self.b_stop.setToolTip("Stop, transcribe and pack the session; the Library opens when it lands")
        self.b_stop.clicked.connect(lambda: Recorder.shared().stop())
        br.addWidget(self.b_mark)
        br.addWidget(self.b_stop)
        lay.addWidget(self.buttons)

        self.b_force = Button("Force stop (skip packing, keep raw files)", size=12, height=30, radius=9,
                              color=Theme.secondary, expanding=True)
        self.b_force.clicked.connect(lambda: Recorder.shared().force_stop())
        lay.addWidget(self.b_force)

        wrap = QWidget()
        outer = hbox(wrap)
        outer.addStretch()
        outer.addWidget(surface, 0, Qt.AlignmentFlag.AlignBottom)
        return wrap

    def _open_room(self):
        from PySide6.QtCore import QUrl
        from PySide6.QtGui import QDesktopServices
        rec = Recorder.shared()
        url = AppSettings.shared().portal_page(rec.active_room or AppSettings.shared().portalRoom)
        if url:
            QDesktopServices.openUrl(QUrl(url))

    def set_collapsed(self, collapsed):
        self.collapsed = collapsed
        self.stack.setCurrentWidget(self.pill if collapsed else self.panel)
        self.adjustSize()

    def refresh(self):
        rec = Recorder.shared()
        s = AppSettings.shared()
        finishing = rec.phase == Phase.finishing
        clock = clock_string(rec.elapsed)
        self.elapsed.setText(clock)
        set_color(self.elapsed, Theme.secondary if finishing else Theme.text)
        self.pill_text.setText("Packing…" if finishing else clock)
        self.pill_dot.setVisible(not finishing)
        self.dot.setVisible(not finishing)
        self.state.setText(rec.mode.packing_label if finishing else rec.mode.label)

        if rec.mode == RecordMode.tab:
            source = " — ".join(p for p in (rec.tab_title, rec.tab_url) if p) or "attaching to the tab…"
        elif rec.mode == RecordMode.window:
            source = rec.source or "picking the screen…"
        else:
            source = rec.where_label
        self.source.set_text(source)
        self.source.setVisible(not finishing)
        self.facts.set_text(" · ".join(rec.facts))
        self.facts.setVisible(not finishing and bool(rec.facts))

        self.narration.setVisible(not finishing and s.liveNarrationHUD)
        if rec.hearing:
            self.narration.setText(rec.hearing)
            set_color(self.narration, Theme.secondary)
        elif rec.last_narration:
            self.narration.setText(rec.last_narration)
            set_color(self.narration, Theme.text)
        else:
            self.narration.setText("waiting for speech…")
            set_color(self.narration, Theme.muted)

        self.finishing.setVisible(finishing)
        self.finishing.setText("%s  ·  %s" % (clock_string(rec.finishing_elapsed), rec.finishing_status or "stopping…"))

        text, leaves = data_summary(rec.data_lines)
        self.data.set_text(text)
        self.data.setToolTip("\n".join(t for t, _ in rec.data_lines))
        set_color(self.data, Theme.amber if leaves else Theme.faint)
        self.b_room.setVisible(not finishing and s.portal_active)
        live = rec.system_audio_available
        self.b_sys.setVisible(not finishing and live)
        self.b_sys.setText("mute system audio" if rec.system_audio else "unmute system audio")
        self.b_sys.setFixedWidth(self.b_sys.sizeHint().width())
        self.b_sys.setToolTip("%s is mixed into the narration; click to mute it" % rec.system_audio_source
                              if rec.system_audio else
                              "%s is muted; click to mix it back in" % rec.system_audio_source)
        self.buttons.setVisible(not finishing)
        audio = rec.mode == RecordMode.audio
        self.b_mark.setVisible(not audio)
        self.b_stop.setToolTip("Stop; recgo finalizes the file and uploads it where configured" if audio else
                               "Stop, transcribe and pack the session; the Library opens when it lands")
        self.b_force.setText("Force stop" if audio else "Force stop (skip packing, keep raw files)")
        self.b_force.setVisible(finishing and rec.finishing_elapsed > 8)
        self.adjustSize()

    def showEvent(self, e):
        super().showEvent(e)
        self.adjustSize()

    def place(self):
        screen = QApplication.primaryScreen()
        if screen is None or QApplication.platformName().startswith("wayland"):
            return
        f = screen.availableGeometry()
        self.adjustSize()
        saved = AppSettings.shared().geometry("hud")
        if saved:
            try:
                x, y = (int(v) for v in str(saved).split(","))
                if f.contains(x + 20, y + 20):
                    self.move(x, y)
                    return
            except ValueError:
                pass
        self.move(f.right() - HUD_WIDTH - 36, f.bottom() - self.height() - 82)
