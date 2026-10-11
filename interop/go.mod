// Test-only module: github.com/cB-Sigma/mbus-sim's wireless meter against
// wmbusmeters in Docker (see doc.go). Not part of the published module;
// nothing here is imported by it.
module github.com/cB-Sigma/mbus-sim/interop

go 1.27.1

require (
	github.com/cB-Sigma/mbus v0.1.0
	github.com/cB-Sigma/mbus-sim v0.0.0
)

require (
	github.com/cB-Sigma/dlms v0.1.0 // indirect
	github.com/cB-Sigma/iotcrypto v0.1.1 // indirect
	github.com/cB-Sigma/tlsx v0.6.0 // indirect
	github.com/goburrow/serial v0.1.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/cB-Sigma/mbus-sim => ../
