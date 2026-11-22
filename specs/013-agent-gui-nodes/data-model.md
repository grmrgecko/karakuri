# Data model

Window targets contain an exact ID, regex match, or active selector. Exact IDs win; non-list operations reject multiple matches. Snapshots contain a timestamp, window metadata, nullable enabled state, geometry availability, backend names, and protected-value-filtered elements.

Desktop controls retain provider IDs, text, class, geometry, enabled/visible/protected state, and read errors. Menus contain ID, text, path, enabled state, nullable checked state, and child items. Dialog results contain window/control observations and per-window inspection failures. Display results use the existing provider DTO.

State options contain text, role, optional element ID, enabled/gone predicates or a stable pixel interval and window-relative rectangle. Timeout is positive and exceeds stability time. State failure evidence records the condition and original failure, with screenshot and snapshot errors separately.

Window selectors add match mode, title, class, PID, and executable. State options add enabled-state selection and single-check behavior. Snapshots may contain native controls and per-source truncation. Dialog options contain optional window scope and a controls preference. Control targets add role, immediate parent, and match mode. Inline source is transient host-local data, removed after execution.
