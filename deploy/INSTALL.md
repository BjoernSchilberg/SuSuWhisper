# SuSuWhisper auf dem VPS (Debian/Ubuntu)

SuSuWhisper läuft als abgeschotteter systemd-Dienst auf 127.0.0.1:8080,
Caddy davor kümmert sich um HTTPS. Die Dateien in diesem Ordner:

| Datei | Ziel auf dem VPS |
|---|---|
| `Caddyfile` | `/etc/caddy/Caddyfile` |
| `susuwhisper.service` | `/etc/systemd/system/susuwhisper.service` |
| `susuwhisper.logrotate` | `/etc/logrotate.d/susuwhisper` |

Vorher ersetzen: `susuwhisper.example.org` und `admin@example.org` in der
`Caddyfile`. Der DNS-Eintrag (A/AAAA) der Domain muss auf den VPS zeigen.
Im Folgenden steht `vps` für den SSH-Zugang zum Server.

## 1. Programm bauen (im Repo, auf deinem Rechner)

```sh
git switch main && git pull --ff-only
# ARM-VPS: GOARCH=arm64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o susuwhisper .
ssh vps mkdir -p /tmp/susu
scp -r susuwhisper *.html tinymce deploy/Caddyfile deploy/susuwhisper.service \
    deploy/susuwhisper.logrotate vps:/tmp/susu/
```

## 2. SuSuWhisper einrichten (auf dem VPS, als root)

```sh
useradd --system --no-create-home --shell /usr/sbin/nologin susuwhisper

# Programm und Vorlagen gehören root und sind für den Dienst nur lesbar
install -d -m 755 /opt/susuwhisper
install -m 755 /tmp/susu/susuwhisper /opt/susuwhisper/
install -m 644 /tmp/susu/*.html /opt/susuwhisper/
cp -r /tmp/susu/tinymce /opt/susuwhisper/
chown -R root:root /opt/susuwhisper
chmod -R a+rX /opt/susuwhisper/tinymce

# Nur diese drei Verzeichnisse darf der Dienst beschreiben
install -d -o susuwhisper -g susuwhisper -m 750 \
    /opt/susuwhisper/data /opt/susuwhisper/uploads /opt/susuwhisper/logs
```

Vorhandene Artikel übernehmen (optional): `articles.json` nach
`/opt/susuwhisper/data/`, die Unterordner von `uploads/` nach
`/opt/susuwhisper/uploads/`, danach
`chown -R susuwhisper:susuwhisper /opt/susuwhisper/data /opt/susuwhisper/uploads`.

```sh
install -m 644 /tmp/susu/susuwhisper.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now susuwhisper
systemctl status susuwhisper
curl -I http://127.0.0.1:8080/overview        # erwartet: 200 OK
systemd-analyze security susuwhisper          # erwartet: etwa 1.1 OK

install -m 644 /tmp/susu/susuwhisper.logrotate /etc/logrotate.d/susuwhisper
logrotate -d /etc/logrotate.d/susuwhisper     # Probelauf, ändert nichts
```

## 3. Caddy einrichten

Caddy nach https://caddyserver.com/docs/install installieren (offizielles
Paketrepo). Dann:

```sh
cp /tmp/susu/Caddyfile /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
```

## 4. Firewall

```sh
ufw allow OpenSSH           # zuerst, sonst sperrst du dich aus
ufw allow 80,443/tcp
ufw allow 443/udp           # HTTP/3, optional
ufw enable
```

Port 8080 bleibt zu; SuSuWhisper lauscht ohnehin nur auf 127.0.0.1.

## Aktualisieren

Neues Programm bauen und kopieren wie in Schritt 1, dann:

```sh
install -m 755 /tmp/susu/susuwhisper /opt/susuwhisper/
systemctl restart susuwhisper
```

## Sichern

Alle Daten liegen in `/opt/susuwhisper/data` und `/opt/susuwhisper/uploads`.
