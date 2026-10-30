FROM golang:1.27.3-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /out/oracle ./cmd/oracle

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/oracle /usr/local/bin/oracle

EXPOSE 8080

ENTRYPOINT ["oracle"]
CMD ["api"]
