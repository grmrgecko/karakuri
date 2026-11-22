# Node contract: Graceful process termination

Run Command and Run Script add a Termination grace duration field with ID termination_grace and default 0s. Values are duration strings from zero through one minute. For example, 250ms and 5s are valid. Invalid strings, negative values, and values above one minute fail before side effects. Windows rejects positive values explicitly.

Unix zero grace sends SIGKILL immediately to the existing dedicated process group. Positive grace sends SIGTERM once, observes group disappearance, and sends SIGKILL to any remaining members at the deadline. The process leader exiting alone does not end cleanup.

Cancellation/timeout never become success because the child exits zero. No automatic retry is added. Node timeout remains the execution deadline; the wrapper allows termination_grace plus its existing ten-second margin afterward. Parent cancellation stops waiting immediately while cleanup may continue.

Automation Script retains its existing settings and immediate forced cancellation. Existing node outputs, accepted exits, stream collection, and result placement are unchanged.
