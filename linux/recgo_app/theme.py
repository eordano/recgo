from PySide6.QtGui import QColor, QFont, QFontDatabase, QPalette
from PySide6.QtWidgets import QApplication


def hexc(v, alpha=1.0):
    c = QColor(v)
    c.setAlphaF(alpha)
    return c


def white(alpha):
    return QColor(255, 255, 255, int(alpha * 255))


def black(alpha):
    return QColor(0, 0, 0, int(alpha * 255))


def tinted(c, alpha):
    return QColor(c.red(), c.green(), c.blue(), int(alpha * 255))


class Theme:
    bg = hexc("#161518")
    surface = hexc("#201E25")
    card = black(0.34)
    accent = hexc("#FF2D55")
    accent_hover = hexc("#FF4B6E")
    pink = hexc("#FF859C")
    amber = hexc("#FFBC5B")
    green = hexc("#30CD00")
    text = hexc("#FCFCFC")
    secondary = hexc("#CFCDD4")
    muted = hexc("#A09BA8")
    faint = hexc("#716B7C")
    click_blue = hexc("#A0ABFF")
    focus_purple = hexc("#C640CD")
    shot_green = hexc("#34CE76")
    net_orange = hexc("#FF7439")
    net_text = hexc("#FF9E7A")
    hairline = white(0.10)
    row_hover = white(0.10)
    control = white(0.08)
    list_bg = hexc("#1A191F")
    selection = tinted(accent, 0.16)


def css(c):
    return "rgba(%d,%d,%d,%.3f)" % (c.red(), c.green(), c.blue(), c.alphaF())


_families = {}


def family(mono):
    key = "mono" if mono else "sans"
    if key not in _families:
        wanted = (["JetBrains Mono", "Fira Code", "IBM Plex Mono", "DejaVu Sans Mono"] if mono
                  else ["Inter", "Noto Sans", "Cantarell", "DejaVu Sans"])
        have = set(QFontDatabase.families())
        pick = next((w for w in wanted if w in have), None)
        if pick is None:
            pick = QFontDatabase.systemFont(
                QFontDatabase.SystemFont.FixedFont if mono else QFontDatabase.SystemFont.GeneralFont).family()
        _families[key] = pick
    return _families[key]


def font(size, weight=QFont.Weight.Normal, mono=False):
    f = QFont(family(mono))
    f.setPixelSize(int(size))
    f.setWeight(weight)
    if mono:
        f.setStyleHint(QFont.StyleHint.Monospace)
    f.setHintingPreference(QFont.HintingPreference.PreferNoHinting)
    return f


def arrow_icon_path():
    import os
    from PySide6.QtCore import QRectF
    from PySide6.QtGui import QPainter, QPen, QPixmap
    base = os.environ.get("XDG_RUNTIME_DIR") or os.environ.get("TMPDIR") or "/tmp"
    path = os.path.join(base, "recgo-chevron.png")
    if not os.path.exists(path):
        pm = QPixmap(20, 12)
        pm.fill(QColor(0, 0, 0, 0))
        p = QPainter(pm)
        p.setRenderHint(QPainter.RenderHint.Antialiasing)
        p.setPen(QPen(QColor("#CFCDD4"), 2))
        p.drawLine(3, 3, 10, 9)
        p.drawLine(10, 9, 17, 3)
        p.end()
        pm.save(path)
    return path


def apply_dark(app: QApplication):
    app.setStyle("Fusion")
    arrow = arrow_icon_path()
    app.setFont(font(13))
    p = QPalette()
    p.setColor(QPalette.ColorRole.Window, Theme.bg)
    p.setColor(QPalette.ColorRole.WindowText, Theme.text)
    p.setColor(QPalette.ColorRole.Base, Theme.surface)
    p.setColor(QPalette.ColorRole.AlternateBase, Theme.bg)
    p.setColor(QPalette.ColorRole.Text, Theme.text)
    p.setColor(QPalette.ColorRole.Button, Theme.surface)
    p.setColor(QPalette.ColorRole.ButtonText, Theme.text)
    p.setColor(QPalette.ColorRole.Highlight, Theme.accent)
    p.setColor(QPalette.ColorRole.HighlightedText, Theme.text)
    p.setColor(QPalette.ColorRole.ToolTipBase, Theme.surface)
    p.setColor(QPalette.ColorRole.ToolTipText, Theme.text)
    p.setColor(QPalette.ColorRole.PlaceholderText, Theme.faint)
    p.setColor(QPalette.ColorGroup.Disabled, QPalette.ColorRole.Text, Theme.faint)
    p.setColor(QPalette.ColorGroup.Disabled, QPalette.ColorRole.ButtonText, Theme.faint)
    p.setColor(QPalette.ColorGroup.Disabled, QPalette.ColorRole.WindowText, Theme.faint)
    app.setPalette(p)
    mono = family(True)
    sans = family(False)
    app.setStyleSheet(f"""
        QWidget {{ font-family: "{sans}"; }}
        QToolTip {{ background: {css(Theme.surface)}; color: {css(Theme.text)}; font-size: 12px;
                    border: 1px solid {css(white(0.12))}; padding: 5px 8px; border-radius: 6px; }}
        QLineEdit, QComboBox {{ background: {css(white(0.07))}; color: {css(Theme.text)}; font-size: 13px;
                    border: 1px solid {css(white(0.09))}; border-radius: 8px; padding: 5px 10px;
                    selection-background-color: {css(Theme.accent)}; }}
        QLineEdit:focus, QComboBox:focus {{ border: 1px solid {css(tinted(Theme.accent, 0.7))}; }}
        QLineEdit[mono="true"], QComboBox[mono="true"] {{ font-family: "{mono}"; font-size: 12px; }}
        QComboBox {{ padding-right: 26px; }}
        QComboBox::drop-down {{ subcontrol-origin: padding; subcontrol-position: center right; border: none;
                    width: 24px; }}
        QComboBox::down-arrow {{ image: url({arrow}); width: 10px; height: 6px; }}
        QComboBox:disabled {{ color: {css(Theme.faint)}; }}
        QComboBox QAbstractItemView {{ background: {css(Theme.surface)}; color: {css(Theme.text)};
                    selection-background-color: {css(Theme.accent)}; border: 1px solid {css(white(0.12))}; }}
        QScrollArea {{ border: none; background: transparent; }}
        QScrollBar:vertical {{ background: transparent; width: 10px; margin: 2px; }}
        QScrollBar::handle:vertical {{ background: {css(white(0.14))}; border-radius: 3px; min-height: 24px; }}
        QScrollBar::handle:vertical:hover {{ background: {css(white(0.24))}; }}
        QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {{ height: 0; }}
        QScrollBar::add-page, QScrollBar::sub-page {{ background: transparent; }}
        QScrollBar:horizontal {{ background: transparent; height: 10px; margin: 2px; }}
        QScrollBar::handle:horizontal {{ background: {css(white(0.14))}; border-radius: 3px; min-width: 24px; }}
        QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {{ width: 0; }}
        QMessageBox {{ background: {css(Theme.bg)}; }}
        QMessageBox QLabel {{ color: {css(Theme.text)}; font-size: 13px; }}
        QPushButton {{ background: {css(white(0.08))}; color: {css(Theme.text)}; border: none; font-size: 13px;
                    border-radius: 9px; padding: 7px 13px; font-weight: 600; min-width: 64px; }}
        QPushButton:hover {{ background: {css(white(0.14))}; }}
        QPushButton:default {{ background: {css(Theme.accent)}; }}
        QPushButton:disabled {{ color: {css(Theme.faint)}; }}
        QMenu {{ background: {css(Theme.surface)}; color: {css(Theme.text)}; border: 1px solid {css(white(0.12))};
                 border-radius: 10px; padding: 6px; font-size: 13px; }}
        QMenu::item {{ padding: 7px 14px; border-radius: 7px; }}
        QMenu::item:selected {{ background: {css(white(0.10))}; }}
        QMenu::item:disabled {{ color: {css(Theme.faint)}; }}
        QMenu::separator {{ height: 1px; background: {css(white(0.08))}; margin: 6px 4px; }}
    """)
