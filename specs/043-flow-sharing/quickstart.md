# Quickstart: Verify flow sharing

## Prerequisites

- Build with `make dev`, then run `systemctl --user restart karakuri-dev.service`.
  Confirm the service is active and http://localhost:8080 answers.
- Sign in as `admin` / `password123`. Get the MCP token with
  `./karakuri token list admin --name=mcp --raw` from `build/`.
- Create two disposable accounts, `sharetest-a` and `sharetest-b`, in the
  default User group. Delete them and their flows when done.

## 1. Ownership (US1, SC-001)

1. As `sharetest-a`, create a flow "Porch test" with a manual trigger and
   deploy it.
2. As `sharetest-b`, request `GET /api/flows`. "Porch test" is absent.
3. As `sharetest-b`, request `GET /api/flows/{id}`, `/versions`, `/run`, and
   `DELETE /api/flows/{id}`. Each returns 404.
4. As `admin`, request `GET /api/flows`. "Porch test" is listed.

## 2. Edit share (US2, SC-003)

1. As `sharetest-a`, `POST /api/flows/{id}/shares` with
   `{"username":"sharetest-b","permission":"edit"}`. The response is 201.
2. As `sharetest-b`, save a graph change and deploy. Both succeed, and the
   new version names `sharetest-b`.
3. As `sharetest-b`, add a share or delete the flow. Each returns 403.
4. Post a share with `"permission":"owner"`. The response is 400 and nothing
   is stored.

## 3. View share and live streams (US3, SC-005)

1. Change the share to `view`. As `sharetest-b`, open the flow in the
   browser. The graph is shown with no save, deploy, or run controls. Take a
   screenshot and check the console.
2. Remove the share. Within 30 seconds, a debug socket that `sharetest-b`
   had open on the flow stops receiving its run messages, and
   `GET /api/events` no longer lists "Porch test" entries for them.

## 4. Subflows (US5, SC-004)

1. As `admin`, create a shared subflow and share it with `sharetest-a` at
   view. As `sharetest-a`, reference it from "Porch test", deploy, and run.
   The run succeeds.
2. Remove the share. Run "Porch test" again: it still runs. As
   `sharetest-a`, add a second reference to that subflow in a draft. The
   save is refused.

## 5. Dashboard trigger binding (SC-006)

As `sharetest-b` with no share on "Porch test", create a dashboard with a
flow-trigger button bound to it and deploy. The deploy is refused with the
flow named.

## 6. Tests and handoff (SC-007)

Run `make test TARGET=server` and `make test TARGET=frontend`. Restart the
service and confirm it answers. Report the disposable accounts and flows
removed.
