// Package protocol records how the committed Nanit protobuf bindings are generated.
package protocol

//go:generate protoc -I ../.. --go_out=../.. --go_opt=module=github.com/keatsfonam/nanit-controller internal/protocol/nanit.proto
