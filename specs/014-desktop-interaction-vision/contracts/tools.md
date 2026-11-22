# Tool contracts

Desktop adds Drag(ctx, DragRequest) and PlaceWindow(ctx, WindowInfo, WindowPlacement). Both use one helper attempt. Implementers of Desktop must implement the added methods; enrolled state and existing node configurations do not change.

CLI adds drag X1 Y1 X2 Y2 [--duration seconds] [--button name], window restore TARGET, window place TARGET X Y WIDTH HEIGHT, and image-match IMAGE REFERENCE [--minimum-match PERCENT] [--max-results N]. These commands are accepted in command files.

Agent GUI adds Drag and Drop, Window Placement, and Image Match. Drag preserves the input message and attaches operation metadata; placement returns requested and observed data. Image Match returns object observations and selects conditional binary image/reference value sockets or fresh captures and a reference path. Selected binary sockets require value edges. Standalone MCP tests supply binary fields as base64 configuration literals. Empty completed observations are successful; dependencies, format, cancellation, and resource limits are errors. Image analysis never clicks automatically.
