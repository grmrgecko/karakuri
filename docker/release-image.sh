#!/bin/sh
# Builds or pushes an image assembled from release archives in dist/. The
# Makefile's docker-release and docker-agent-release targets build with it, and
# docker-push and docker-agent-push push with it, so the server and agent images
# are unpacked, tagged, and published the same way.
#
#   release-image.sh build   needs IMAGE, NAME, DOCKERFILE, CONTEXT, PLATFORMS,
#                            HOST_ARCH; FILES, BUILD_ARGS, and ENSURE_DIRS are
#                            optional.
#   release-image.sh push    needs IMAGE.
#
# NAME is the archive's name before its version (karakuri or karakuri-agent).
# FILES are copied into the context beside the Dockerfile. PLATFORMS is a
# comma-separated buildx platform list. ENSURE_DIRS are directories created in
# each unpacked archive when it lacks them, for a COPY of an optional part such
# as the runtimes an archive built with PYTHON_SCRIPTS=0 leaves out.
set -eu

action=${1:-}

case "$action" in
push)
    tag=$(docker images --format '{{.Tag}}' "$IMAGE" | grep -v '^latest$' | head -1)
    if [ -z "$tag" ]; then
        echo "Error: no $IMAGE image built; build it with make first" >&2
        exit 1
    fi
    echo "==> Pushing $IMAGE:$tag"
    docker push "$IMAGE:$tag"
    case "$tag" in
    *snapshot*) ;;
    *) docker push "$IMAGE:latest" ;;
    esac
    exit 0
    ;;
build) ;;
*)
    echo "usage: release-image.sh build|push" >&2
    exit 2
    ;;
esac

# Unpack one archive per platform. A platform with no archive is an error
# rather than a skip: the image would otherwise ship without it and nothing
# would say so.
rm -rf "$CONTEXT"
mkdir -p "$CONTEXT"
cp "$DOCKERFILE" "$CONTEXT/Dockerfile"
for file in ${FILES:-}; do
    cp "$file" "$CONTEXT/"
done

tag=
for platform in $(echo "$PLATFORMS" | tr ',' ' '); do
    arch=${platform#linux/}
    archive=
    # The server's archives are matched by the digit that starts every
    # version, so karakuri-* never picks up karakuri-agent-*.
    for candidate in dist/"$NAME"-[0-9]*.linux-"$arch".tar.gz; do
        [ -f "$candidate" ] && archive=$candidate
    done
    if [ -z "$archive" ]; then
        echo "Error: no $NAME archive for linux-$arch in dist/; run make snapshot or make release first" >&2
        exit 1
    fi
    base=${archive##*/}
    base=${base%%.linux-*}
    version=${base#"$NAME"-}
    if [ -n "$tag" ] && [ "$tag" != "$version" ]; then
        echo "Error: dist/ holds $NAME $tag and $version; clean it and rebuild one version" >&2
        exit 1
    fi
    tag=$version
    mkdir -p "$CONTEXT/$arch"
    tar xzf "$archive" -C "$CONTEXT/$arch" --strip-components=1
    for dir in ${ENSURE_DIRS:-}; do
        mkdir -p "$CONTEXT/$arch/$dir"
    done
done
echo "==> Unpacked $NAME $tag for $PLATFORMS into $CONTEXT"

# A snapshot is labelled as one and never claims latest.
set -- -t "$IMAGE:$tag"
case "$tag" in
*snapshot*) echo "    snapshot, so it is not tagged latest" ;;
*) set -- "$@" -t "$IMAGE:latest" ;;
esac
for arg in ${BUILD_ARGS:-}; do
    set -- "$@" --build-arg "$arg"
done

# buildx builds each platform and, with the containerd image store, loads them
# as one multi-platform image that docker push publishes as one tag. Without
# buildx only the host's own platform can be built.
if docker buildx version >/dev/null 2>&1; then
    case "$PLATFORMS" in
    *,*)
        if ! docker info -f '{{json .DriverStatus}}' | grep -q containerd.snapshotter; then
            echo "Error: loading a multi-platform image needs the containerd image store; enable it in the daemon, or pass DOCKER_PLATFORMS=linux/$HOST_ARCH" >&2
            exit 1
        fi
        ;;
    esac
    echo "==> Building $IMAGE:$tag for $PLATFORMS"
    docker buildx build --platform "$PLATFORMS" --load "$@" "$CONTEXT"
elif [ "$PLATFORMS" = "linux/$HOST_ARCH" ]; then
    echo "==> Building $IMAGE:$tag for $HOST_ARCH"
    docker build --build-arg TARGETARCH="$HOST_ARCH" "$@" "$CONTEXT"
else
    echo "Error: building $PLATFORMS needs docker buildx; install it, or pass DOCKER_PLATFORMS=linux/$HOST_ARCH" >&2
    exit 1
fi
echo "==> Built $IMAGE:$tag"
