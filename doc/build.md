## Compilation

Prerequisies:

* Go >= 1.23
* C Compiler (GCC / Clang / ...)
* Zig >= 0.14.0 (optional, for cross-compiling CLI versions)
* binutils (optional, for building Windows GUI version)

Get the source code:

    git clone https://github.com/nkanaev/yarr.git

Compile:

    # create cli for the host OS/architecture
    make host               # out/yarr

    # create GUI, works only in the target OS
    make windows_amd64_gui  # out/windows_amd64_gui/yarr.exe
    make windows_arm64_gui  # out/windows_arm64_gui/yarr.exe
    make darwin_arm64_gui   # out/darwin_arm64_gui/yarr.app
    make darwin_amd64_gui   # out/darwin_amd64_gui/yarr.app

    # create cli, cross-compiles within any OS/architecture
    make linux_amd64
    make linux_arm64
    make linux_armv7
    make windows_amd64
    make windows_arm64

    # ... or build a docker image
    docker build -t yarr -f etc/dockerfile .

## Release builds

Pushing a `v*` tag triggers the Build workflow, which builds and uploads ZIP
archives to the GitHub Release for that tag:

* macOS arm64 and amd64: GUI (`yarr.app`) and CLI
* Windows amd64: GUI (`yarr.exe`) and CLI
* Windows arm64 and Linux amd64, arm64, and armv7: CLI

GUI archives use the `yarr_<os>_<arch>_gui.zip` naming convention. Windows arm64
GUI is not built, matching upstream. Manual workflow runs on branches build
artifacts without publishing a Release; select a `v*` tag to publish a Release.

## ARM compilation

The instructions below are to cross-compile *yarr* to `Linux/ARM*`.

Build:

    docker build -t yarr.arm -f etc/dockerfile.arm .

Test:

    # inside host
    docker run -it --rm yarr.arm

    # then, inside container
    cd /root/out
    qemu-aarch64 -L /usr/aarch64-linux-gnu/ yarr.arm64

Extract files from images:

    CID=$(docker create yarr.arm)
    docker cp -a "$CID:/root/out" .
    docker rm "$CID"
