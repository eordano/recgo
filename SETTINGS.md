# Settings Panel

The settings panel allows users to configure recgo without editing the config file manually.

## Usage

### Opening the Settings Panel

Press `c` to open the settings panel from the main view.

### Navigation

- `j` or `↓` - Move down to the next setting
- `k` or `↑` - Move up to the previous setting
- `g` - Jump to the first setting
- `G` - Jump to the last setting
- `Enter` - Edit the selected setting
- `c` or `Esc` - Close the settings panel

### Editing Settings

1. Navigate to the setting you want to change using `j`/`k`
2. Press `Enter` to start editing
3. Type the new value
4. Press `Enter` to save the change, or `Esc` to cancel

### Saving Changes

Settings are modified in memory when you edit them. To persist changes to the config file:

- Press `Ctrl+S` while in the settings panel (or from the main view)

A warning will appear at the bottom of the settings panel if there are unsaved changes.

## Available Settings

### Recording

- **Output Directory**: Directory where recordings are saved
- **Format**: File format (mkv, opus, wav)
- **Audio Codec**: Audio codec (aac, opus, flac)
- **Bitrate**: Bitrate (e.g., 128k, 192k, 256k)

### UI

- **Show VU Meters**: Show/hide VU meters (true, false)
- **Refresh Rate (FPS)**: UI refresh rate in FPS (1-60)

## Implementation Details

The settings panel is implemented in `internal/ui/settings.go` and follows the existing btop-inspired styling.

### Key Features

- **Vim-like navigation**: Familiar keybindings for power users
- **In-place editing**: Edit settings directly in the TUI using text input
- **Visual feedback**:
  - Selected items are highlighted
  - Current category is shown with a separator
  - Help text appears for each field
  - Modified indicator shows when changes haven't been saved
- **Validation**: Settings are validated before being saved
- **Centered layout**: The settings panel is centered on screen for better readability

### Code Structure

The `SettingsPanel` struct maintains:
- Reference to the config
- Current mode (navigation or edit)
- Selected field index
- Text input component for editing
- Modified flag to track unsaved changes

The panel integrates with the existing `Model` through:
- A new `ModeSettings` app mode
- Keybindings for opening/closing (`c` key)
- Keybinding for saving config (`Ctrl+S`)
- Rendering in full-screen overlay mode
