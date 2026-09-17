import os

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from PySide6.QtWidgets import QApplication

from recgo_app.settings_view import PANES, SettingsWindow


def _app():
    return QApplication.instance() or QApplication([])


def test_every_pane_has_a_heading_and_searchable_rows():
    _app()
    w = SettingsWindow()
    assert w.stack.count() == len(PANES) == len(w._entries)
    assert all(rows for rows in w._entries)
    for rows in w._entries:
        for row in rows:
            assert row.search_text and row.title_text


def test_search_hides_rows_and_panes_then_restores():
    _app()
    w = SettingsWindow()
    w.show()
    w.search.setText("whisper")
    QApplication.processEvents()
    visible_panes = [i for i, (row, _) in enumerate(w.side_rows) if not row.isHidden()]
    names = [PANES[i][0] for i in visible_panes]
    assert names == ["Transcription"]
    assert w.stack.currentIndex() == visible_panes[0]
    shown = [r for r in w._entries[visible_panes[0]] if not r.isHidden()]
    assert shown and all("whisper" in r.search_text for r in shown)
    assert any("<span" in (r.title.text() + getattr(r, "caption", r.title).text()) for r in shown)
    w.search.clear()
    QApplication.processEvents()
    assert all(not row.isHidden() for row, _ in w.side_rows)
    assert all(not r.isHidden() for rows in w._entries for r in rows)
    assert "<span" not in w._entries[0][0].title.text() + w._entries[0][0].caption.text()
    w.close()
