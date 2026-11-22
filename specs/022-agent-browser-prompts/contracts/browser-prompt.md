# Browser prompt contract

Node type browser-prompt: In accepts any message; one output per buttons[].id
carries the original envelope plus meta.browser_prompt. Timeout and browser launch failure use normal engine Error routing.
Defaults: title Help needed, timeout 15m, buttons retry/Retry, bypass/Bypass, stop/Stop.
An optional browser_base_url overrides server.base_url; use a URL reachable from
that agent, not localhost on a different machine.

GET /api/browser_prompts/current returns title, rendered HTML, button records,
feedback requirements, expiry, server time and attachment metadata. The browser
uses server time for the countdown so agent clock skew does not end it early. POST
/api/browser_prompts/response accepts {button,text}. GET
/api/browser_prompts/assets/{name} serves a matching attachment. Each needs an
Authorization: Bearer request-token header. Invalid/unavailable tokens return 410;
invalid response data returns 400 without consuming the request. Duplicate replies
return 410. Bodies are bounded. Prompt tokens cannot authenticate ordinary APIs.

/prompt#token is an account-free page. Raw HTML is omitted by goldmark. Custom
attachment references are ![alt](attachment:key) for raster images and
[label](attachment:key) for files. Files download under their original filename.
