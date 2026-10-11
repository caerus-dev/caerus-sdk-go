package proto

//go:generate protoc -I ../../proto --go_out=srepb --go_opt=paths=source_relative --go_opt=Msre_service.proto=github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb --go-grpc_out=srepb --go-grpc_opt=paths=source_relative --go-grpc_opt=Msre_service.proto=github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb sre_service.proto
//go:generate protoc -I ../../proto --go_out=dlspb --go_opt=paths=source_relative --go_opt=Mdls_service.proto=github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb --go-grpc_out=dlspb --go-grpc_opt=paths=source_relative --go-grpc_opt=Mdls_service.proto=github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb dls_service.proto
