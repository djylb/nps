export GOPROXY=direct
GIT_VERSION=$(git describe --tags --abbrev=14 --match "v[0-9]*" 2>/dev/null | sed 's/^v//')
LDFLAGS="$(LDFLAGS) -X 'ehang.io/nps/lib/version/version.VERSION=${GIT_VERSION}'"


sudo apt-get update
sudo apt-get install gcc-mingw-w64-i686 gcc-multilib
env GOOS=windows GOARCH=386 CGO_ENABLED=1 CC=i686-w64-mingw32-gcc go build -ldflags "$(LDFLAGS)" -buildmode=c-shared -o npc_sdk.dll cmd/npc/sdk.go
env GOOS=linux GOARCH=386 CGO_ENABLED=1 CC=gcc go build -ldflags "$(LDFLAGS)" -buildmode=c-shared -o npc_sdk.so cmd/npc/sdk.go
tar -czvf npc_sdk.tar.gz npc_sdk.dll npc_sdk.so npc_sdk.h

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)"  ./cmd/npc/npc.go

tar -czvf linux_amd64_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_386_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=freebsd GOARCH=386 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf freebsd_386_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf freebsd_amd64_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=freebsd GOARCH=arm go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf freebsd_arm_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_arm_v7_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_arm_v6_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_arm_v5_client.tar.gz npc conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_arm64_client.tar.gz npc conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=linux GOARCH=mips64 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_mips64_client.tar.gz npc conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=linux GOARCH=mips64le go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_mips64le_client.tar.gz npc conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=linux GOARCH=mipsle go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_mipsle_client.tar.gz npc conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=linux GOARCH=mips go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf linux_mips_client.tar.gz npc conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf windows_386_client.tar.gz npc.exe conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf windows_amd64_client.tar.gz npc.exe conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf darwin_amd64_client.tar.gz npc conf/npc.conf conf/multi_account.conf

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" ./cmd/npc/npc.go

tar -czvf darwin_arm64_client.tar.gz npc conf/npc.conf conf/multi_account.conf


CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_amd64_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps

CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_386_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_arm_v5_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_arm_v6_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_arm_v7_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_arm64_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=freebsd GOARCH=arm go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf freebsd_arm_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=freebsd GOARCH=386 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf freebsd_386_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf freebsd_amd64_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps



CGO_ENABLED=0 GOOS=linux GOARCH=mips go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_mips_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=linux GOARCH=mips64 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_mips64_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=linux GOARCH=mips64le go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_mips64le_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=linux GOARCH=mipsle go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf linux_mipsle_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps



CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf darwin_amd64_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps


CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf darwin_arm64_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps



CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf windows_amd64_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps.exe


CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -ldflags "$(LDFLAGS)" ./cmd/nps/nps.go

tar -czvf windows_386_server.tar.gz conf/nps.conf conf/tasks.json conf/clients.json conf/hosts.json conf/server.key  conf/server.pem web/views web/static nps.exe