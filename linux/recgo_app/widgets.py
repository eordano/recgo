import html
import subprocess

from PySide6.QtCore import QPoint, QPointF, QRectF, QSize, Qt, QTimer, Signal
from PySide6.QtGui import (QColor, QDesktopServices, QFont, QIcon, QKeySequence, QPainter, QPainterPath, QPen,
                           QPixmap)
from PySide6.QtCore import QUrl
from PySide6.QtWidgets import (QAbstractButton, QFrame, QHBoxLayout, QLabel, QSizePolicy, QVBoxLayout,
                               QWidget)

from recgo_app.theme import Theme, css, font, white


def label(text, size=13, weight=QFont.Weight.Normal, color=Theme.text, mono=False, wrap=False,
          selectable=False):
    lb = QLabel(text)
    lb.setFont(font(size, weight, mono))
    lb.setStyleSheet("color: %s; background: transparent;" % css(color))
    lb.setWordWrap(wrap)
    if wrap:
        lb.setSizePolicy(QSizePolicy.Policy.Preferred, QSizePolicy.Policy.Minimum)
    if selectable:
        lb.setTextInteractionFlags(Qt.TextInteractionFlag.TextSelectableByMouse)
    return lb


def rich_label(html_text, size=13, color=Theme.text, mono=False, weight=QFont.Weight.Normal):
    lb = QLabel(html_text)
    lb.setTextFormat(Qt.TextFormat.RichText)
    lb.setFont(font(size, weight, mono))
    lb.setStyleSheet("color: %s; background: transparent;" % css(color))
    lb.setWordWrap(True)
    lb.setSizePolicy(QSizePolicy.Policy.Ignored, QSizePolicy.Policy.Minimum)
    lb.setTextInteractionFlags(Qt.TextInteractionFlag.TextSelectableByMouse)
    return lb


def mono_field(edit):
    edit.setProperty("mono", "true")
    edit.setFont(font(12, mono=True))
    return edit


class ElidedLabel(QLabel):
    def __init__(self, text, size=13, weight=QFont.Weight.Normal, color=Theme.text, mono=False, parent=None):
        super().__init__(parent)
        self.full = text
        self.setFont(font(size, weight, mono))
        self.setStyleSheet("color: %s; background: transparent;" % css(color))
        self.setSizePolicy(QSizePolicy.Policy.Ignored, QSizePolicy.Policy.Fixed)
        self.setFixedHeight(self.fontMetrics().height() + 2)
        self.setToolTip(text)

    def set_text(self, text):
        if text == self.full:
            return
        self.full = text
        self.setToolTip(text)
        self.setText(self.fontMetrics().elidedText(self.full, Qt.TextElideMode.ElideMiddle, self.width()))

    def resizeEvent(self, e):
        self.setText(self.fontMetrics().elidedText(self.full, Qt.TextElideMode.ElideMiddle, self.width()))
        super().resizeEvent(e)


def set_color(widget, color):
    widget.setStyleSheet("color: %s; background: transparent;" % css(color))


def hline():
    f = QFrame()
    f.setFixedHeight(1)
    f.setStyleSheet("background: %s;" % css(Theme.hairline))
    return f


def vline():
    f = QFrame()
    f.setFixedWidth(1)
    f.setStyleSheet("background: %s;" % css(Theme.hairline))
    return f


class HoverRow(QWidget):
    clicked = Signal()

    def __init__(self, radius=11, base=None, hover=None, parent=None):
        super().__init__(parent)
        self.radius = radius
        self.base = base or QColor(0, 0, 0, 0)
        self.hover = hover or Theme.row_hover
        self._over = False
        self.setAttribute(Qt.WidgetAttribute.WA_Hover)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setSizePolicy(QSizePolicy.Policy.Expanding, QSizePolicy.Policy.Fixed)

    def set_base(self, color):
        self.base = color
        self.update()

    def enterEvent(self, e):
        self._over = True
        self.update()

    def leaveEvent(self, e):
        self._over = False
        self.update()

    def mouseReleaseEvent(self, e):
        if e.button() == Qt.MouseButton.LeftButton and self.rect().contains(e.position().toPoint()):
            self.clicked.emit()

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        p.setPen(Qt.PenStyle.NoPen)
        p.setBrush(self.hover if self._over else self.base)
        p.drawRoundedRect(QRectF(self.rect()), self.radius, self.radius)


class Card(QFrame):
    def __init__(self, fill=None, stroke=None, radius=13, parent=None):
        super().__init__(parent)
        self.fill = fill or Theme.card
        self.stroke = stroke or white(0.07)
        self.radius = radius

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        p.setPen(QPen(self.stroke, 1))
        p.setBrush(self.fill)
        p.drawRoundedRect(QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5), self.radius,
                          self.radius)


class RecDot(QWidget):
    def __init__(self, size=9, parent=None):
        super().__init__(parent)
        self.setFixedSize(size, size)
        self._phase = 0.0
        self._timer = QTimer(self)
        self._timer.setInterval(40)
        self._timer.timeout.connect(self._step)
        self._timer.start()

    def _step(self):
        self._phase = (self._phase + 0.05) % 2.0
        self.update()

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        k = self._phase if self._phase <= 1 else 2 - self._phase
        c = QColor(Theme.accent)
        c.setAlphaF(0.28 + 0.72 * k)
        p.setPen(Qt.PenStyle.NoPen)
        p.setBrush(c)
        p.drawEllipse(self.rect())


class Caret(QWidget):
    def __init__(self, parent=None):
        super().__init__(parent)
        self.setFixedSize(2, 14)
        self._on = True
        t = QTimer(self)
        t.setInterval(500)
        t.timeout.connect(self._flip)
        t.start()

    def _flip(self):
        self._on = not self._on
        self.update()

    def paintEvent(self, e):
        if not self._on:
            return
        p = QPainter(self)
        p.fillRect(self.rect(), Theme.accent)


class Toggle(QAbstractButton):
    def __init__(self, parent=None):
        super().__init__(parent)
        self.setCheckable(True)
        self.setFixedSize(40, 23)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setFocusPolicy(Qt.FocusPolicy.TabFocus)

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        p.setPen(Qt.PenStyle.NoPen)
        on = self.isChecked()
        p.setBrush(Theme.accent if on else white(0.16))
        if not self.isEnabled():
            p.setOpacity(0.5)
        p.drawRoundedRect(QRectF(0, 0, 40, 23), 11.5, 11.5)
        if self.hasFocus():
            p.setPen(QPen(white(0.35), 1))
            p.setBrush(Qt.BrushStyle.NoBrush)
            p.drawRoundedRect(QRectF(0.5, 0.5, 39, 22), 11.5, 11.5)
            p.setPen(Qt.PenStyle.NoPen)
        p.setBrush(Theme.text)
        x = 40 - 2 - 19 if on else 2
        p.drawEllipse(QRectF(x, 2, 19, 19))


class Segment(QWidget):
    changed = Signal(str)

    def __init__(self, options, current, parent=None, size=12, bold=False, radius=8):
        super().__init__(parent)
        self.options = options
        self.current = current
        self._size = size
        self._bold = bold
        self._radius = radius
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self._fm_font = font(size, QFont.Weight.Bold if bold else QFont.Weight.DemiBold)
        self.setFont(self._fm_font)
        self.setFixedHeight(28)
        self.enabled_keys = None

    def _cells(self):
        fm = self.fontMetrics()
        x = 2
        cells = []
        for key, text in self.options:
            w = fm.horizontalAdvance(text) + 22
            cells.append((key, text, QRectF(x, 2, w, self.height() - 4)))
            x += w + 2
        return cells

    def sizeHint(self):
        cells = self._cells()
        return QSize(int(cells[-1][2].right() + 2) if cells else 40, 28)

    def set_current(self, key):
        self.current = key
        self.update()

    def mouseReleaseEvent(self, e):
        for key, _, r in self._cells():
            if r.contains(e.position()):
                if self.enabled_keys is not None and key not in self.enabled_keys:
                    return
                if key != self.current:
                    self.current = key
                    self.update()
                    self.changed.emit(key)
                return

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        p.setPen(Qt.PenStyle.NoPen)
        p.setBrush(QColor(0, 0, 0, int(0.3 * 255)))
        p.drawRoundedRect(QRectF(self.rect()), self._radius + 2, self._radius + 2)
        for key, text, r in self._cells():
            active = key == self.current
            if active:
                p.setPen(Qt.PenStyle.NoPen)
                p.setBrush(white(0.18))
                p.drawRoundedRect(r, self._radius, self._radius)
            disabled = self.enabled_keys is not None and key not in self.enabled_keys
            color = Theme.text if active else (QColor(Theme.muted) if not disabled else QColor(Theme.faint))
            if disabled and not active:
                color.setAlphaF(0.5)
            p.setPen(color)
            p.drawText(r, Qt.AlignmentFlag.AlignCenter, text)


class Button(QAbstractButton):
    def __init__(self, text, fill=None, hover=None, color=None, size=13, bold=False, radius=12,
                 height=38, parent=None, expanding=False):
        super().__init__(parent)
        self.setText(text)
        self.fill = fill or Theme.control
        self.hover_fill = hover or white(0.14)
        self.color = color or Theme.text
        self.radius = radius
        self.setFont(font(size, QFont.Weight.Bold if bold else QFont.Weight.DemiBold))
        self.setFixedHeight(height)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setAttribute(Qt.WidgetAttribute.WA_Hover)
        self._over = False
        if expanding:
            self.setSizePolicy(QSizePolicy.Policy.Expanding, QSizePolicy.Policy.Fixed)

    def sizeHint(self):
        return QSize(self.fontMetrics().horizontalAdvance(self.text()) + 26, self.height())

    def set_fill(self, fill):
        self.fill = fill or Theme.control
        self.update()

    def enterEvent(self, e):
        self._over = True
        self.update()

    def leaveEvent(self, e):
        self._over = False
        self.update()

    def paintEvent(self, e):
        p = QPainter(self)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        p.setPen(Qt.PenStyle.NoPen)
        p.setBrush(self.hover_fill if self._over else self.fill)
        if not self.isEnabled():
            p.setOpacity(0.5)
        p.drawRoundedRect(QRectF(self.rect()), self.radius, self.radius)
        p.setPen(self.color)
        p.drawText(self.rect(), Qt.AlignmentFlag.AlignCenter, self.text())


def highlight(text, query):
    esc = html.escape(text)
    q = query.strip()
    if not q:
        return esc
    out = []
    low = text.lower()
    ql = q.lower()
    i = 0
    while True:
        j = low.find(ql, i)
        if j < 0:
            out.append(html.escape(text[i:]))
            break
        out.append(html.escape(text[i:j]))
        out.append('<span style="background:%s;color:%s">%s</span>'
                   % (css(QColor(255, 188, 91, 82)), css(Theme.text), html.escape(text[j:j + len(q)])))
        i = j + len(q)
    return "".join(out)


def open_path(path):
    QDesktopServices.openUrl(QUrl.fromLocalFile(path))


def reveal(path):
    try:
        subprocess.Popen(["busctl", "--user", "--timeout=3", "call", "org.freedesktop.FileManager1",
                          "/org/freedesktop/FileManager1", "org.freedesktop.FileManager1",
                          "ShowItems", "ass", "1", QUrl.fromLocalFile(path).toString(), ""],
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except OSError:
        open_path(path)


def tray_pixmap(state, size=64):
    """The one recording indicator: a thin ring while idle, the ring filled
    red while recording, a grey fill while the session packs."""
    pm = QPixmap(size, size)
    pm.fill(QColor(0, 0, 0, 0))
    p = QPainter(pm)
    p.setRenderHint(QPainter.RenderHint.Antialiasing)
    c = size / 2
    r = size * 0.40
    p.setPen(QPen(QColor("#E6E4EA"), size * 0.08))
    p.setBrush(Qt.BrushStyle.NoBrush)
    p.drawEllipse(QRectF(c - r, c - r, 2 * r, 2 * r))
    if state != "idle":
        p.setPen(Qt.PenStyle.NoPen)
        p.setBrush(Theme.accent if state == "recording" else QColor("#8E8A97"))
        rr = size * 0.24
        p.drawEllipse(QRectF(c - rr, c - rr, 2 * rr, 2 * rr))
    p.end()
    return pm


def document_icon_path(size):
    """A sheet with a folded corner: the session document."""
    path = QPainterPath()
    x0, y0 = size * 0.24, size * 0.16
    w, h, fold = size * 0.52, size * 0.68, size * 0.16
    path.moveTo(x0, y0)
    path.lineTo(x0 + w - fold, y0)
    path.lineTo(x0 + w, y0 + fold)
    path.lineTo(x0 + w, y0 + h)
    path.lineTo(x0, y0 + h)
    path.closeSubpath()
    return path


def app_icon_pixmap(size=256):
    """A document with a red record dot: a recording that becomes a document,
    not a camera app."""
    pm = QPixmap(size, size)
    pm.fill(QColor(0, 0, 0, 0))
    p = QPainter(pm)
    p.setRenderHint(QPainter.RenderHint.Antialiasing)
    bg = QPainterPath()
    bg.addRoundedRect(QRectF(0, 0, size, size), size * 0.22, size * 0.22)
    p.fillPath(bg, Theme.surface)
    p.setPen(QPen(QColor("#F2F1F5"), size * 0.05, Qt.PenStyle.SolidLine, Qt.PenCapStyle.RoundCap,
                  Qt.PenJoinStyle.RoundJoin))
    p.setBrush(Qt.BrushStyle.NoBrush)
    p.drawPath(document_icon_path(size))
    p.setPen(QPen(QColor("#F2F1F5"), size * 0.04, Qt.PenStyle.SolidLine, Qt.PenCapStyle.RoundCap))
    for i, frac in enumerate((0.42, 0.56)):
        p.drawLine(QPointF(size * 0.35, size * 0.16 + size * 0.68 * frac),
                   QPointF(size * (0.65 if i else 0.58), size * 0.16 + size * 0.68 * frac))
    p.setPen(Qt.PenStyle.NoPen)
    p.setBrush(Theme.accent)
    rr = size * 0.11
    p.drawEllipse(QRectF(size * 0.62 - rr, size * 0.68 - rr, 2 * rr, 2 * rr))
    p.end()
    return pm


def glyph_pixmap(kind, size=32, color=None):
    """Monochrome line glyphs for the menu when the icon theme has none."""
    pm = QPixmap(size, size)
    pm.fill(QColor(0, 0, 0, 0))
    p = QPainter(pm)
    p.setRenderHint(QPainter.RenderHint.Antialiasing)
    pen = QPen(color or QColor("#CFCDD4"), size * 0.08, Qt.PenStyle.SolidLine, Qt.PenCapStyle.RoundCap,
               Qt.PenJoinStyle.RoundJoin)
    p.setPen(pen)
    p.setBrush(Qt.BrushStyle.NoBrush)
    u = size / 32
    if kind == "screen":
        p.drawRoundedRect(QRectF(4 * u, 6 * u, 24 * u, 16 * u), 2 * u, 2 * u)
        p.drawLine(QPointF(12 * u, 26 * u), QPointF(20 * u, 26 * u))
    elif kind == "window":
        p.drawRoundedRect(QRectF(4 * u, 6 * u, 24 * u, 16 * u), 2 * u, 2 * u)
        p.drawLine(QPointF(12 * u, 26 * u), QPointF(20 * u, 26 * u))
        p.setBrush(pen.color())
        p.drawRect(QRectF(15 * u, 9 * u, 10 * u, 10 * u))
    elif kind == "browser":
        p.drawEllipse(QRectF(5 * u, 5 * u, 22 * u, 22 * u))
        p.drawLine(QPointF(5 * u, 16 * u), QPointF(27 * u, 16 * u))
        p.drawEllipse(QRectF(11 * u, 5 * u, 10 * u, 22 * u))
    elif kind == "tab":
        p.drawRoundedRect(QRectF(4 * u, 8 * u, 24 * u, 18 * u), 2 * u, 2 * u)
        p.drawLine(QPointF(4 * u, 14 * u), QPointF(28 * u, 14 * u))
        p.drawLine(QPointF(10 * u, 8 * u), QPointF(10 * u, 14 * u))
    elif kind == "audio":
        p.drawRoundedRect(QRectF(12 * u, 4 * u, 8 * u, 14 * u), 4 * u, 4 * u)
        p.drawLine(QPointF(8 * u, 15 * u), QPointF(8 * u, 16 * u))
        p.drawArc(QRectF(8 * u, 8 * u, 16 * u, 16 * u), 180 * 16, 180 * 16)
        p.drawLine(QPointF(16 * u, 24 * u), QPointF(16 * u, 28 * u))
    elif kind == "mark":
        p.drawLine(QPointF(9 * u, 5 * u), QPointF(9 * u, 27 * u))
        p.drawPolyline([QPointF(9 * u, 6 * u), QPointF(24 * u, 6 * u), QPointF(19 * u, 11 * u),
                        QPointF(24 * u, 16 * u), QPointF(9 * u, 16 * u)])
    elif kind == "stop":
        p.setBrush(pen.color())
        p.drawRoundedRect(QRectF(8 * u, 8 * u, 16 * u, 16 * u), 2 * u, 2 * u)
    elif kind == "library":
        p.drawPath(document_icon_path(size))
    elif kind == "settings":
        p.drawEllipse(QRectF(11 * u, 11 * u, 10 * u, 10 * u))
        for i in range(8):
            import math
            a = i * math.pi / 4
            p.drawLine(QPointF(16 * u + 9 * u * math.cos(a), 16 * u + 9 * u * math.sin(a)),
                       QPointF(16 * u + 13 * u * math.cos(a), 16 * u + 13 * u * math.sin(a)))
    elif kind == "quit":
        p.drawArc(QRectF(6 * u, 6 * u, 20 * u, 20 * u), 60 * 16, 240 * 16)
        p.drawLine(QPointF(16 * u, 4 * u), QPointF(16 * u, 15 * u))
    elif kind == "hud":
        p.drawRoundedRect(QRectF(4 * u, 10 * u, 24 * u, 12 * u), 4 * u, 4 * u)
    elif kind == "live":
        p.drawPath(document_icon_path(size))
        p.drawLine(QPointF(12 * u, 16 * u), QPointF(20 * u, 16 * u))
        p.drawLine(QPointF(12 * u, 21 * u), QPointF(18 * u, 21 * u))
    elif kind == "leaves":
        p.setPen(QPen(Theme.amber, size * 0.08, Qt.PenStyle.SolidLine, Qt.PenCapStyle.RoundCap,
                      Qt.PenJoinStyle.RoundJoin))
        p.drawPolyline([QPointF(14 * u, 8 * u), QPointF(6 * u, 8 * u), QPointF(6 * u, 26 * u),
                        QPointF(24 * u, 26 * u), QPointF(24 * u, 18 * u)])
        p.drawLine(QPointF(14 * u, 18 * u), QPointF(27 * u, 5 * u))
        p.drawPolyline([QPointF(19 * u, 5 * u), QPointF(27 * u, 5 * u), QPointF(27 * u, 13 * u)])
    elif kind == "folder":
        p.drawPolygon([QPointF(4 * u, 8 * u), QPointF(12 * u, 8 * u), QPointF(14 * u, 11 * u),
                       QPointF(28 * u, 11 * u), QPointF(28 * u, 25 * u), QPointF(4 * u, 25 * u)])
    p.end()
    return pm


THEME_ICONS = {
    "screen": "video-display", "window": "window", "browser": "internet-web-browser",
    "tab": "tab-new", "audio": "audio-input-microphone", "mark": "flag", "stop": "media-playback-stop",
    "library": "folder-documents", "settings": "configure", "quit": "application-exit",
    "hud": "view-visible", "live": "document-preview", "folder": "folder-open",
}


def icon(kind):
    """The desktop theme's icon for the verb, so the menu looks like every
    other Plasma menu, else a line glyph of our own. "leaves" is always ours:
    the amber arrow is the one colour signal, and a theme must not recolour it."""
    ic = QIcon() if kind == "leaves" else QIcon.fromTheme(THEME_ICONS.get(kind, ""))
    if ic.isNull():
        ic = QIcon(glyph_pixmap(kind))
    return ic


def vbox(parent=None, margins=(0, 0, 0, 0), spacing=0):
    lay = QVBoxLayout(parent) if parent is not None else QVBoxLayout()
    lay.setContentsMargins(*margins)
    lay.setSpacing(spacing)
    return lay


def hbox(parent=None, margins=(0, 0, 0, 0), spacing=0):
    lay = QHBoxLayout(parent) if parent is not None else QHBoxLayout()
    lay.setContentsMargins(*margins)
    lay.setSpacing(spacing)
    return lay


_ = QPoint


class ShortcutCapture(QWidget):
    """KDE's key-sequence widget: click, press the chord, done; a second
    button clears it. Emits the chord in the app's lower-case spec form."""
    changed = Signal(str)

    def __init__(self, spec, parent=None):
        super().__init__(parent)
        self.spec = spec
        self.capturing = False
        lay = hbox(self, spacing=6)
        self.button = Button("", size=13, height=32, radius=8, color=Theme.secondary)
        self.button.setMinimumWidth(150)
        self.button.setFocusPolicy(Qt.FocusPolicy.StrongFocus)
        self.button.clicked.connect(self.start)
        self.button.keyPressEvent = self._key
        self.button.focusOutEvent = self._blur
        lay.addWidget(self.button)
        self.clear_btn = Button("✕", size=12, height=32, radius=8, color=Theme.faint)
        self.clear_btn.setFixedWidth(32)
        self.clear_btn.setToolTip("Remove the shortcut")
        self.clear_btn.clicked.connect(lambda: self.set_spec(""))
        lay.addWidget(self.clear_btn)
        self._render()

    def set_spec(self, spec):
        self.capturing = False
        if spec != self.spec:
            self.spec = spec
            self.changed.emit(spec)
        self._render()

    def _render(self):
        from recgo_app import hotkeys
        if self.capturing:
            self.button.setText("Input…")
            self.button.color = Theme.amber
        else:
            chord = hotkeys.parse(self.spec) if self.spec else None
            self.button.setText(chord.label if chord else (self.spec or "None"))
            self.button.color = Theme.text if chord else (Theme.amber if self.spec else Theme.faint)
        self.clear_btn.setVisible(bool(self.spec) and not self.capturing)
        self.button.update()

    def start(self):
        if not self.isEnabled():
            return
        self.capturing = True
        self.button.setFocus()
        self.button.grabKeyboard()
        self._render()

    def _blur(self, e):
        if self.capturing:
            self.capturing = False
            self.button.releaseKeyboard()
            self._render()

    def _key(self, e):
        if not self.capturing:
            return
        key = e.key()
        if key == Qt.Key.Key_Escape:
            self.button.releaseKeyboard()
            self.set_spec(self.spec)
            return
        if key in (Qt.Key.Key_Control, Qt.Key.Key_Shift, Qt.Key.Key_Alt, Qt.Key.Key_Meta,
                   Qt.Key.Key_AltGr, Qt.Key.Key_unknown):
            return
        mods = e.modifiers()
        parts = []
        if mods & Qt.KeyboardModifier.MetaModifier:
            parts.append("meta")
        if mods & Qt.KeyboardModifier.ControlModifier:
            parts.append("ctrl")
        if mods & Qt.KeyboardModifier.AltModifier:
            parts.append("alt")
        if mods & Qt.KeyboardModifier.ShiftModifier:
            parts.append("shift")
        if key == Qt.Key.Key_Backspace and not parts:
            self.button.releaseKeyboard()
            self.set_spec("")
            return
        name = QKeySequence(key).toString(QKeySequence.SequenceFormat.PortableText).lower()
        if not name or not parts:
            return
        self.button.releaseKeyboard()
        self.set_spec("+".join(parts + [name]))
