module github.com/edgegrid/edgegrid/coordinator

go 1.24.0

require (
	github.com/edgegrid/edgegrid/apps/shared v0.0.0-20250808070339-3c1265051286
	github.com/joho/godotenv v1.5.1
	github.com/nats-io/nats.go v1.39.0
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.47.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
)

replace github.com/edgegrid/edgegrid/apps/shared => ../shared
