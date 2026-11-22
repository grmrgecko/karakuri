# Time-series test databases

`make tsdb-up` starts these containers; `make tsdb-down` stops them and
discards their data. `make test TARGET=tsdb` runs the tagged suite in this
directory against them, and `make tsdb-seed` writes the fixture series the
chart checks draw.

| Service | Address | Credentials | Connection settings |
| --- | --- | --- | --- |
| Prometheus | http://localhost:19090 | none | kind `prometheus`, writing on, remote write receiver enabled |
| VictoriaMetrics | http://localhost:19428 | none | kind `prometheus` (query, remote write at `/api/v1/write`) or `influxdb` v1 (`/write`) |
| InfluxDB 1.8 | http://localhost:19086 | `admin` / `karakuri-test`, database `karakuri` | kind `influxdb`, version 1 |
| InfluxDB 2 | http://localhost:19087 | org `karakuri`, bucket `karakuri`, token `karakuri-test-token` | kind `influxdb`, version 2 |
| InfluxDB 3 Core | http://localhost:19181 | no auth, database `karakuri` (created on first write) | kind `influxdb`, version 3 |
| TimescaleDB (PostgreSQL 16) | localhost:19432 | `karakuri` / `karakuri-test`, database `karakuri` | kind `postgres`, sslmode `disable` |

These credentials exist only in the throwaway containers.
