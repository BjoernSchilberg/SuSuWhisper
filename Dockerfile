# Verwende ein offizielles Golang-Image als Basis
FROM golang:1.24-alpine AS build

# Arbeitsverzeichnis im Container
WORKDIR /app

# Kopiere Go-Module-Dateien
COPY go.mod ./

# Lade die Go-Module herunter
RUN go mod download

# Kopiere den Quellcode
COPY *.go ./
COPY *.html ./
COPY tinymce/ ./tinymce/

# Erstelle einen Ordner für die Daten (wird später als Volume gemountet)
RUN mkdir -p data
RUN mkdir -p uploads

# Kompiliere die Anwendung
RUN CGO_ENABLED=0 GOOS=linux go build -o susuwhisper .

# Zweite Schicht für eine kleinere Image-Größe
FROM alpine:latest

WORKDIR /app

# Kopiere die kompilierte Anwendung aus dem Build-Stage
COPY --from=build /app/susuwhisper .
COPY --from=build /app/*.html ./
COPY --from=build /app/tinymce/ ./tinymce/

# Erstelle die notwendigen Verzeichnisse
RUN mkdir -p data
RUN mkdir -p uploads

# Lege die Volumes für die persistenten Daten fest
VOLUME ["/app/data", "/app/uploads"]

# Port, auf dem die Anwendung laufen wird
EXPOSE 8080

# Starte die Anwendung
CMD ["./susuwhisper"]