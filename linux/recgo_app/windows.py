from PySide6.QtGui import QGuiApplication

from PySide6.QtGui import QGuiApplication

from recgo_app import kwin
from recgo_app.recorder import Recorder
from recgo_app.settings import AppSettings


def on_wayland():
    return QGuiApplication.platformName().startswith("wayland")


def saved_hud_pos():
    saved = AppSettings.shared().geometry("hud")
    if not saved:
        return None
    try:
        x, y = (int(v) for v in str(saved).split(","))
    except ValueError:
        return None
    return (x, y)
from recgo_app.settings import AppSettings


def on_wayland():
    return QGuiApplication.platformName().startswith("wayland")


def saved_hud_pos():
    saved = AppSettings.shared().geometry("hud")
    if not saved:
        return None
    try:
        x, y = (int(v) for v in str(saved).split(","))
    except ValueError:
        return None
    return (x, y)


class Windows:
    _instance = None

    @classmethod
    def shared(cls):
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        self.hud = None
        self.live = None
        self.library = None
        self.settings = None
        self.kwin_script = None

    def _ensure_kwin(self, reload=False):
        if self.kwin_script is None or reload:
            self.kwin_script = kwin.load(saved_hud_pos())

    @property
    def hud_visible(self):
        return self.hud is not None and self.hud.isVisible()

    def show_hud(self):
        self._ensure_kwin(reload=True)
        if self.hud is None:
            from recgo_app.hud import HUDWindow
            self.hud = HUDWindow(self)
            self.hud.place()
        self.hud.show()

    def close_hud(self):
        if self.hud is not None:
            self.hud.hide()
            self.hud.deleteLater()
            self.hud = None

    def toggle_hud(self):
        if self.hud_visible:
            self.close_hud()
        elif Recorder.shared().is_recording:
            self.show_hud()

    @property
    def live_visible(self):
        return self.live is not None and self.live.isVisible()

    @property
    def hud_collapsed(self):
        return self.hud is not None and self.hud.collapsed

    def show_live(self):
        self._ensure_kwin()
        if self.live is None:
            from recgo_app.live import LiveWindow
            self.live = LiveWindow()
            self.live.place()
            self.live.closed.connect(self._live_closed)
        self.live.show()
        self.live.raise_()
        self._aux_opened()

    def _live_closed(self):
        self._aux_closed()

    def close_live(self):
        if self.live is not None:
            self.live.hide()
            self.live.deleteLater()
            self.live = None
        self._live_closed()

    def toggle_live(self):
        if self.live_visible:
            self.close_live()
        else:
            self.show_live()

    # While recording, a big window on top folds the HUD to its pill; closing
    # the last one brings the full HUD back, re-showing it if it was hidden.
    def _aux_opened(self):
        if Recorder.shared().is_recording and self.hud is not None:
            self.hud.set_collapsed(True)

    def _aux_closed(self):
        if not Recorder.shared().is_recording:
            return
        if self.aux_visible:
            return
        self.show_hud()
        self.hud.set_collapsed(False)

    @property
    def aux_visible(self):
        return any(w is not None and w.isVisible() for w in (self.library, self.settings, self.live))

    def show_library(self, selecting=None):
        from recgo_app.library import LibraryStore, LibraryWindow
        if self.library is None:
            self.library = LibraryWindow()
            self.library.closed.connect(self._aux_closed)
        LibraryStore.shared().reload(selecting)
        self.library.show()
        self.library.raise_()
        self.library.activateWindow()
        self._aux_opened()

    def show_settings(self):
        if self.settings is None:
            from recgo_app.settings_view import SettingsWindow
            self.settings = SettingsWindow()
            self.settings.closed.connect(self._aux_closed)
        self.settings.show()
        self.settings.raise_()
        self.settings.activateWindow()
        self._aux_opened()

    def close_auxiliary(self):
        if self.library is not None:
            self.library.close()
        if self.settings is not None:
            self.settings.close()
        self.close_live()

    def shutdown(self):
        self.close_hud()
        self.close_live()
        if self.kwin_script:
            kwin.unload()
