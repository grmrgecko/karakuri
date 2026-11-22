# Research: Desktop interaction and vision

## Drag and placement

Decision: use native desktop providers and one serialized drag RPC. Existing separate mouse holds are insufficient for cleanup and concurrency. Source APIs support window-manager requests, not guaranteed bounds. Generic Wayland foreign-toplevel offers restore but no position/size operation; set_rectangle describes a taskbar representation and must not be used for placement. Hyprland/Sway require explicit existing floating state.

Sources: [EWMH](https://specifications.freedesktop.org/wm/latest-single/), [SetWindowPos](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setwindowpos), [Apple AX writes](https://developer.apple.com/documentation/applicationservices/1460434-axuielementsetattributevalue), [Hyprland dispatchers](https://wiki.hypr.land/configuring/core/dispatchers/), [Sway](https://github.com/swaywm/sway/blob/master/sway/sway.5.scd).

## Image matching

Decision: same-scale RGB absolute-difference percentage matching in Go, with bounded comparisons and overlapping detection merging. Neural recognition, FFT libraries, rotation, and automatic scale searches add unnecessary dependencies for reference-icon matching. A pixel cap alone is insufficient; a 64x64 reference over 1920x1080 can need billions of comparisons. Early rejection improves typical cases but does not replace a hard work budget. Pixel bounds remain image-relative because Retina and Wayland scaling make desktop addition unsafe.

Source: [template matching model](https://docs.opencv.org/4.x/d4/dc6/tutorial_py_template_matching.html).

The local Hyprland 0.56.2 session uses Lua dispatchers; the legacy pixel dispatcher
syntax is rejected. Version detection selects the API before mutation. Resize
precedes movement because the compositor can center a floating window on resize.
