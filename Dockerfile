# syntax=docker/dockerfile:1

# Один образ, два бинаря: api и migrator собираются из одного коммита,
# поэтому версия схемы и версия кода не могут разъехаться (фактор I).
# Какой из них запускать, решает compose через entrypoint.

# Версия Go совпадает с go.mod до патча: иначе toolchain скачивался бы
# на каждой сборке заново.
FROM golang:1.26.4-alpine AS build

WORKDIR /src
COPY . .

# CGO выключен: бинарь статический и запускается в образе без libc.
# Кэши модулей и сборки живут в cache mount, а не в слоях образа —
# инструменты из tool-директив go.mod в образ не попадают вовсе.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
        -o /out/ ./cmd/api ./cmd/migrator

# distroless/static: ни шелла, ни пакетного менеджера — даже при удачной
# атаке в контейнере нечем воспользоваться. Корневые сертификаты внутри
# есть, без них не заработали бы ни S3, ни STARTTLS к почтовому серверу.
FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/api /out/migrator /usr/local/bin/

USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/api"]
