# Installing Karakuri

Karakuri is one server program with a built-in web interface. Install it once,
create an admin account, and open `http://<host>:8080`. This guide covers each
way to install it and how to point it at SQLite, PostgreSQL, or MySQL.

## Choosing a method

| Method | Use it when |
| --- | --- |
| Docker Compose | You run containers. One file, restarts after a reboot, updates with a pull. |
| Docker Compose with PostgreSQL | The same, with a PostgreSQL container instead of SQLite. |
| Release archive | Linux or macOS without containers. Installs a system service that updates itself. |
| Unpacked, without installing | Windows, or trying Karakuri from a directory. |
| From source | You work on Karakuri itself. |

Every method listens on port 8080 for the web interface and API, and on 9443
for remote agents. Each starts on SQLite unless you configure another database.

## Docker Compose

Save the [compose.yaml](https://github.com/grmrgecko/karakuri/blob/main/compose.yaml)
from the repository into an empty directory:

```yaml
services:
  karakuri:
    image: grmrgecko/karakuri:latest
    container_name: karakuri
    restart: unless-stopped
    ports:
      - "8080:8080" # Web interface and API.
      - "9443:9443" # Remote agents dial this port.
    volumes:
      - karakuri-data:/var/lib/karakuri
      - karakuri-config:/etc/karakuri

volumes:
  karakuri-data:
  karakuri-config:
```

Start it and create the first admin account:

```sh
docker compose up -d
docker compose exec karakuri karakuri user create admin admin@example.com <password> --admin
```

The first start writes `/etc/karakuri/config.yaml` into the `karakuri-config`
volume with fresh secrets. The database and files live in `karakuri-data`. Keep
both volumes: the encryption key in the config is the only key the stored
connection credentials open with.

To update, pull the new image and recreate the container:

```sh
docker compose pull
docker compose up -d
```

A container never updates itself; the pull is the update. Once a day the server
checks for a newer release and shows it on the overview and in the event log, so
you know when to pull. A tool such as Watchtower can pull for you; each upgrade
copies the database before changing it, as
[Every upgrade, however it arrives](configuration.md#every-upgrade-however-it-arrives)
describes. Set up scheduled backups for that copy to be a full archive.

Each release is also published under its version, such as
`grmrgecko/karakuri:0.1.0`. Pin a version in `image:` to choose when to upgrade.
To go back after an upgrade, restore the archive taken before it and run the
older image; the older image refuses to start on the upgraded database on its own.

The image has a healthcheck, so `docker ps` shows whether the server is serving,
and the Compose files give the server 45 seconds to stop. The image runs on
amd64 and arm64 and includes Chromium for dashboard renders.

## Docker Compose with PostgreSQL

Save [compose.postgres.yaml](https://github.com/grmrgecko/karakuri/blob/main/compose.postgres.yaml)
as `compose.yaml` in an empty directory. It runs a `postgres:17-alpine`
container beside Karakuri and points Karakuri at it with environment
variables. Both containers read the database password from a `.env` file
beside the compose file:

```sh
echo "KARAKURI_DB_PASSWORD=$(openssl rand -hex 24)" > .env
docker compose up -d
docker compose exec karakuri karakuri user create admin admin@example.com <password> --admin
```

Karakuri waits for PostgreSQL to answer before it starts, and creates its
tables on the first start. The database lives in the `karakuri-postgres`
volume. Back up `.env` with the volumes: PostgreSQL keeps the password it
started with, so a new `.env` locks Karakuri out of the old database.

Use a hex password like the one above. The connection string separates its
settings with spaces, so a password with spaces or quotes breaks it.

## docker run

Without Compose, the same container starts with:

```sh
docker run -d --name karakuri --restart unless-stopped -p 8080:8080 -p 9443:9443 \
  -v karakuri-data:/var/lib/karakuri -v karakuri-config:/etc/karakuri \
  grmrgecko/karakuri
docker exec -it karakuri karakuri user create admin admin@example.com <password> --admin
```

## Release archive on Linux or macOS

Download the server archive for your platform from
[GitHub releases](https://github.com/grmrgecko/karakuri/releases). Archives are
built for Linux and macOS on amd64 and arm64. Unpack it and run the installer:

```sh
tar xzf karakuri-<version>.linux-amd64.tar.gz
cd karakuri-<version>.linux-amd64
sudo ./install.sh
sudo karakuri service install
sudo karakuri service start
sudo -u karakuri karakuri user create admin admin@example.com <password> --admin
```

`install.sh` puts the binary in `/usr/local/bin`, plugins and themes in
`/usr/share/karakuri`, a configuration with fresh secrets in `/etc/karakuri`,
and the database in `/var/lib/karakuri`. The service runs as the `karakuri`
account. Run `install.sh` without `sudo` to install under your home directory
instead, then run the `karakuri service` commands as yourself.

`karakuri service` also takes `stop`, `restart`, `status`, and `uninstall`, and
works with systemd, OpenRC, SysV, and launchd.

macOS refuses to start a binary unpacked from a browser download until its
quarantine flag is cleared:

```sh
xattr -dr com.apple.quarantine karakuri-<version>.darwin-arm64
```

A system install checks for a newer release once a day from a root timer,
installs it, and rolls back if the new version will not start.
`sudo karakuri update` installs a newer release now.

## Unpacked, without installing

On Windows (amd64), or to run Karakuri from a directory on any platform, unpack
the archive and start it from there:

```sh
mkdir etc
./karakuri config gen --env=prod > etc/config.yaml
./karakuri user create admin admin@example.com <password> --admin
./karakuri serve
```

`config gen` writes placeholder secrets that every generated file shares.
Before anyone else can reach the server, replace `security.jwt_secret` and
`security.encryption_key` in `etc/config.yaml` with the output of
`openssl rand -hex 32`, one run for each. Set `encryption_key` before saving
any connection; changing it later makes stored credentials unreadable.

`karakuri service install` registers the same directory with the Windows
service manager when you want it to run in the background.

## From source

Building needs Go (the version in `go.mod`) and Node.js with npm. Every build
goes through the Makefile:

```sh
make                 # optimized build into build/
sudo make install    # runs install.sh on build/, as for a release
```

`make docker` builds the server image from the checkout.

## Databases

Karakuri stores flows, dashboards, devices, users, and run history in one
database. The `database` section of `config.yaml` chooses it:

| Key | Default | Meaning |
| --- | --- | --- |
| `type` | `sqlite` | `sqlite`, `postgres`, or `mysql`. |
| `path` | `<data directory>/karakuri.db` | The SQLite file. |
| `dsn` | unset | The PostgreSQL or MySQL connection string. |
| `max_open_conns` / `max_idle_conns` | 25 / 5 | The connection pool. |
| `conn_max_lifetime` | 300 | Seconds one connection is reused. |

Every key can also come from an environment variable: `KARAKURI_DATABASE_TYPE`,
`KARAKURI_DATABASE_DSN`, and so on. An environment variable wins over the file,
which is how the PostgreSQL compose file switches a container to PostgreSQL
without editing its generated config. Karakuri creates and updates its tables
itself on start, so the database only needs to exist and be empty.

### SQLite

SQLite needs no setup and is the default. The whole database is one file in the
data directory: `/var/lib/karakuri/karakuri.db` for a system install and in the
Docker image. It suits one house. Choose PostgreSQL or MySQL when you already
run one and back it up, or want the database on another machine.

### PostgreSQL

Create a user and a database it owns:

```sql
CREATE USER karakuri WITH PASSWORD '<password>';
CREATE DATABASE karakuri OWNER karakuri;
```

Then set the database in `config.yaml` and restart the server:

```yaml
database:
  type: postgres
  dsn: "host=db.example port=5432 user=karakuri password=<password> dbname=karakuri sslmode=disable"
```

The URL form works too:
`postgres://karakuri:<password>@db.example:5432/karakuri?sslmode=disable`. Use
`sslmode=require` or `verify-full` when the database is on another machine.

### MySQL and MariaDB

Create a database and a user with every privilege on it:

```sql
CREATE DATABASE karakuri CHARACTER SET utf8mb4;
CREATE USER 'karakuri'@'%' IDENTIFIED BY '<password>';
GRANT ALL PRIVILEGES ON karakuri.* TO 'karakuri'@'%';
```

Then set the database in `config.yaml` and restart the server:

```yaml
database:
  type: mysql
  dsn: "karakuri:<password>@tcp(db.example:3306)/karakuri?charset=utf8mb4&parseTime=True&loc=UTC"
```

Keep `parseTime=True`. Without it the driver hands times back as text and the
server cannot read its own records.

### Moving an existing install to another database

Pointing a running install at an empty database starts it over with nothing in
it. To keep your flows and devices, take a backup under **Settings, Backup**,
change the config to the new database, and restore the backup into it. A backup
holds rows rather than a database file, so it restores from SQLite into
PostgreSQL or MySQL and back. The Backup guide covers restoring.

## First sign-in

Open `http://<host>:8080` and sign in with the account you created. For anyone
reaching the server from another machine, set `server.base_url` to the address
they use, such as `http://karakuri.lan:8080`, so links and dashboard renders
point at it. **Help** in the web interface has every guide, including this one.
