# Quickstart: Home and overview

Prerequisites: build with `make dev`, restart `karakuri-dev.service`, sign in
as `admin` / `password123`, and get the MCP token as `AGENTS.md` describes.

1. **Attention through MCP.** Create a disposable MQTT connection to an
   unreachable host. Call `get_attention`: one `connection` error item
   appears, and `checked` includes `connections`. Delete the connection, call
   again, and the item is gone.
2. **Flow failures.** Create a test flow with a manual trigger into a script
   that throws `boom`. Run it twice with `run_flow_test`. `get_attention`
   shows one flow item with `count: 2`. Fix the script and run it once:
   the item turns into a warning with `recovered: true`.
3. **Upcoming.** Deploy and enable a flow with a schedule node whose cron
   fires within the hour. `list_upcoming` lists it. Disable the flow and it
   is gone.
4. **Unfinished.** Save a draft change on that flow without deploying.
   `list_unfinished_work` lists it; deploying removes it.
5. **Browser.**
   - `/` shows the overview.
   - `/flows` shows the flow list, and Back from the editor returns there.
   - Pin a dashboard from the list; it appears under quick launch.
   - Set the start page to that dashboard in Profile; `/` opens it.
   - Reset the start page.
   - Check the console.
6. **Dashboard-only user.** Sign in as a user whose role has only dashboard
   read and one shared dashboard; sign-in lands on that dashboard.

Delete the test flows and connection afterwards.
