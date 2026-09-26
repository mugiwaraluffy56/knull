FROM golang:1.26.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/checkout-fixture ./cmd/checkout-fixture
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /checkout-fixture ./cmd/checkout-fixture

FROM scratch
USER 65532:65532
COPY --from=build /checkout-fixture /checkout-fixture
EXPOSE 8080
ENTRYPOINT ["/checkout-fixture"]
