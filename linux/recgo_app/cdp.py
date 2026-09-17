import json

from PySide6.QtCore import QObject, QUrl, Signal
from PySide6.QtNetwork import QNetworkAccessManager, QNetworkRequest

UNRECORDABLE = ("devtools://", "chrome://", "chrome-extension://", "edge://", "about:blank")


def recordable_tabs(targets):
    """The page targets recgo-tab can attach to, as {id, title, url} dicts
    in the order the browser lists them."""
    out = []
    for t in targets:
        url = t.get("url", "")
        if t.get("type") != "page" or not t.get("webSocketDebuggerUrl") or not url:
            continue
        if url.startswith(UNRECORDABLE):
            continue
        out.append({"id": t.get("id", ""), "title": t.get("title", ""), "url": url})
    return out


class CDP(QObject):
    changed = Signal(bool)
    _instance = None

    @classmethod
    def shared(cls):
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        super().__init__()
        self.available = False
        self.tabs = []
        self._nam = QNetworkAccessManager(self)
        self._nam.setTransferTimeout(600)

    def tab(self, tab_id):
        for t in self.tabs:
            if t["id"] == tab_id:
                return t
        return None

    def refresh(self, done=None):
        reply = self._nam.get(QNetworkRequest(QUrl("http://127.0.0.1:9222/json/list")))

        def finished():
            status = reply.attribute(QNetworkRequest.Attribute.HttpStatusCodeAttribute)
            up = status == 200
            if up:
                try:
                    self.tabs = recordable_tabs(json.loads(bytes(reply.readAll()).decode("utf-8", "replace")))
                except ValueError:
                    self.tabs = []
            else:
                self.tabs = []
            reply.deleteLater()
            if up != self.available:
                self.available = up
                self.changed.emit(up)
            if done:
                done(up)

        reply.finished.connect(finished)
