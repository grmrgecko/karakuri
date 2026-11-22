# SQL query node contract

`sql-query` is an impure Storage node with Any `in`, Object `out`, and Error outputs.

| Field | Contract |
| --- | --- |
| scope | `flow` (default) or `global` |
| query | One literal SQLite statement; incoming values are never interpolated |
| parameters | Expression returning an array for positional bindings or object for named bindings; default `[]` |
| timeout | Integer seconds, 1–60, default 5 |
| max_rows | Integer row count, 1–10000, default 1000 |

Named object keys use the SQL binding's full name (`:reading`, `@reading`, `$reading`).
Every placeholder must have one value, with no extras. Supported values are null,
strings, booleans, finite numbers, and binary bytes from expressions. Structured
values must be explicitly encoded by the expression. Anonymous `?` bindings use
an array. Positional arrays bind SQLite's numbered positions, including `?NNN`.

Output payload is `{columns: [], rows: [], changes: 0, last_insert_id: 0}`.
Rows use column names as keys, so SELECT expressions should have aliases. Topic,
metadata, and grouping survive. NULL stays null and binary cells use base64.

SELECT, CRUD, RETURNING, and ordinary main-schema DDL are supported. ATTACH,
DETACH, VACUUM, PRAGMA, extensions, file functions, virtual/temporary tables and
transaction control are refused. Supported SQL functions are the reviewed
built-ins listed in the store policy, including normal aggregate/date/JSON use.

Errors preserve the previous snapshot and route normally without automatic retry.
Global scope and draft tests can change real shared data. Private storage identity
comes from the executing flow, including the caller identity for shared subflows.
