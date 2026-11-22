# Agent node contracts

Types are advertised under `agent.<id>.` as usual.

| Type | Behavior |
| --- | --- |
| ui-snapshot | Fresh accessible snapshot of selected window |
| click-control | Unique label or element-ID click |
| wait-window | Wait for window presence or absence |
| wait-ui-state | Wait for accessible predicate or stable pixels; optional failure evidence |
| menu-list | Fresh selected-window menu tree |
| menu-invoke | Invoke unique enabled path or ID |
| display-list | Fresh display geometry/modes |
| display-mode | Temporary primary-output mode |
| dialog-list | Fresh application dialog observations and inspection errors |
| screenshot-now | Fresh execution-triggered screenshot |
| window-list-now | Fresh execution-triggered window list |
| automation-script | Execute configured CLI and host-local command file |

New nodes use ordinary input and error ports. Observations return typed objects or binary images; control and menu actions preserve the input payload with result metadata. Display changes return the resulting display list; Automation Script returns command output. Existing screenshot and window-list pure sources keep their current output ports. Screenshot gains selected-window targeting. Config fields that identify windows and controls accept message interpolation. Wait timeout and stability fields permit fractional seconds.

Configuration additions preserve existing defaults. Shared selectors use window_match_mode (regex/contains/exact), window_title, window_class, window_pid, and window_image. Wait nodes add check_once and on_timeout (error/unmatched), accept fractional seconds, and UI state adds enabled_state (either/enabled/disabled). Snapshot adds source (accessibility/native/both), role, text, and max_results; truncated_sources identifies capped sources. Dialog list adds scope_window and include_controls. Screenshot region adds desktop_coordinates. Click control adds role, parent_id, and text_match (auto/exact/contains). Keyboard adds selectors and activate_window. Automation script adds source (file/inline) and commands, preserving literal inline text.
