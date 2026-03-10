# Bazel 9 Bzlmod Reference: rules_go + rules_oci + gazelle

## Latest Versions (as of research date)

| Module | Latest Version |
|--------|---------------|
| rules_go | 0.60.0 |
| rules_oci | 2.2.7 |
| gazelle | 0.47.0 |
| aspect_bazel_lib | 2.22.5 (transitive dep of rules_oci) |

---

## MODULE.bazel

```starlark
module(
    name = "my_project",
    version = "0.0.0",
)

# ─── Core dependencies ───────────────────────────────────────────────
bazel_dep(name = "rules_go", version = "0.60.0")
bazel_dep(name = "gazelle", version = "0.47.0")
bazel_dep(name = "rules_oci", version = "2.2.7")
bazel_dep(name = "aspect_bazel_lib", version = "2.22.5")

# ─── Go SDK ──────────────────────────────────────────────────────────
# Register a Go SDK. The extension reads go.mod to determine the version.
go_sdk = use_extension("@rules_go//go:extensions.bzl", "go_sdk")
go_sdk.download(version = "1.24.3")
use_repo(go_sdk, "go_toolchains")

register_toolchains("@go_toolchains//:all")

# ─── Go Dependencies (from go.mod) ──────────────────────────────────
go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_mod = "//:go.mod")

# IMPORTANT: You must list every *direct* Go module dependency here.
# Run `bazel mod tidy` to auto-populate this list.
# Example:
# use_repo(
#     go_deps,
#     "com_github_google_go_cmp",
# )

# ─── OCI toolchains ─────────────────────────────────────────────────
oci = use_extension("@rules_oci//oci:extensions.bzl", "oci")

# Pull a base image (distroless/static is common for Go)
oci.pull(
    name = "distroless_static",
    digest = "sha256:3d0f463de06b7ddff27684ec3bfd0b54a4cf451742f5a1b8b994f1715c4b7e43",
    image = "gcr.io/distroless/static",
    platforms = ["linux/amd64", "linux/arm64"],
)
use_repo(oci, "distroless_static")
```

### Key notes on MODULE.bazel:

1. **No WORKSPACE file needed** — Bazel 9 uses bzlmod by default.
2. **`go_sdk.download(version = "...")` vs `go_sdk.from_file(go_mod = ...")`** — You can either pin a version explicitly or let it read from go.mod. Using explicit `download` is clearer.
3. **`use_repo` for go_deps** — Every direct dependency in go.mod needs a corresponding entry. Run `bazel mod tidy` to generate these.
4. **`oci.pull` replaces the old WORKSPACE `oci_pull`** — same attributes, but called as an extension tag.
5. **rules_oci 2.2.7 transitively brings in `aspect_bazel_lib`** — but declaring it explicitly gives you access to `@aspect_bazel_lib//lib:tar.bzl` for the `tar` rule.

---

## BUILD.bazel — Complete Example

```starlark
load("@rules_go//go:def.bzl", "go_binary", "go_library", "go_test")
load("@rules_oci//oci:defs.bzl", "oci_image", "oci_load", "oci_push")
load("@aspect_bazel_lib//lib:tar.bzl", "tar")

# ─── 1. Go library ──────────────────────────────────────────────────
go_library(
    name = "app_lib",
    srcs = ["main.go"],
    importpath = "github.com/example/myapp",
    visibility = ["//visibility:private"],
)

# ─── 2. Go binary ──────────────────────────────────────────────────
go_binary(
    name = "app",
    embed = [":app_lib"],
    # For container images, you typically want a static linux binary:
    goarch = "amd64",
    goos = "linux",
    # Pure Go (no CGO) for static linking:
    pure = "on",
    visibility = ["//visibility:public"],
)

# ─── 3. Go test ─────────────────────────────────────────────────────
go_test(
    name = "app_test",
    srcs = ["main_test.go"],
    embed = [":app_lib"],
)

# ─── 4. Package binary into a tar layer ─────────────────────────────
# rules_oci takes tar files as layers (NOT raw binaries).
# Use aspect_bazel_lib's `tar` rule to create the layer.
tar(
    name = "app_layer",
    srcs = [":app"],
)

# ─── 5. OCI image ──────────────────────────────────────────────────
oci_image(
    name = "image",
    base = "@distroless_static",
    tars = [":app_layer"],
    entrypoint = ["/app"],
    # Optional fields (all accept inline values in the macro):
    # cmd = ["--flag", "value"],
    # env = {"PORT": "8080"},
    # exposed_ports = ["8080/tcp"],
    # user = "nonroot",
    # workdir = "/home/nonroot",
    # labels = {"org.opencontainers.image.source": "https://github.com/example/myapp"},
)

# ─── 6. Load image into local Docker daemon ────────────────────────
# Use: bazel run //:image_load
oci_load(
    name = "image_load",
    image = ":image",
    repo_tags = ["myapp:latest"],
)

# ─── 7. Push image to a remote registry ────────────────────────────
# Use: bazel run //:image_push
oci_push(
    name = "image_push",
    image = ":image",
    repository = "index.docker.io/myorg/myapp",
    remote_tags = ["latest"],
)
```

---

## How `oci_push` Works / Pushing to a Localhost Registry

### Mechanism
`oci_push` is an **executable target** (run with `bazel run`). It:
1. Pushes the image by digest first
2. Then applies each tag from `remote_tags` sequentially
3. Uses standard Docker auth config (`~/.docker/config.json`) by default
4. Uses `crane` under the hood (bundled via `@oci_crane_toolchains`)

### Runtime flag overrides
The generated pusher binary accepts flags:
```bash
# Override repository at runtime:
bazel run //:image_push -- --repository localhost:5000/myapp

# Add additional tags at runtime:
bazel run //:image_push -- --tag custom-tag
```

### Pushing to a localhost registry in a test

**Yes, you can push to a localhost registry.** The rules_oci test suite itself does exactly this using `crane registry serve`. Here's the pattern from their own test:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Start a local ephemeral registry using crane
output=$(mktemp)
$CRANE registry serve --address=localhost:0 >> "$output" 2>&1 &

# Wait for port
timeout=$((SECONDS + 10))
while [ "$SECONDS" -lt "$timeout" ]; do
    port=$(sed -nr 's/.+serving on port ([0-9]+)/\1/p' "$output")
    [ -n "$port" ] && break
done
REGISTRY="localhost:$port"

# Push using the oci_push binary with --repository override
"$PUSH_BINARY" --repository "${REGISTRY}/myapp"

# Verify
"$CRANE" digest "${REGISTRY}/myapp:latest"
```

To set this up as a Bazel `sh_test`:

```starlark
sh_test(
    name = "push_test",
    srcs = ["push_test.sh"],
    args = [
        "$(CRANE_BIN)",
        "$(location :image_push)",
    ],
    data = [
        ":image_push",
        "@oci_crane_toolchains//:current_toolchain",
    ],
    toolchains = [
        "@oci_crane_toolchains//:current_toolchain",
    ],
)
```

Alternatively, for a simpler approach without a test registry, use `oci_load` to load into Docker, then push with standard `docker push`, or just use `oci_push` pointed at `localhost:5000/myapp` (if you have a registry running).

---

## Important API Notes

### `oci_image` (the macro, not `oci_image_rule`)
- `entrypoint`, `cmd`: accept **list of strings** OR a label to a file (one entry per line)
- `env`, `labels`, `annotations`: accept **dict of strings** OR a label to a `key=value` file
- `exposed_ports`, `volumes`: accept **list of strings** OR a label to a file
- `tars`: list of labels to `.tar` files — these become image layers
- `base`: label to another `oci_image` or a pulled image (`@distroless_static`)

### `oci_load` (replaces the old `oci_tarball` name)
- Loads into local Docker/Podman daemon via `bazel run`
- `repo_tags`: list like `["myapp:latest"]`
- `format`: defaults to Docker format; set `"oci"` for OCI format (requires containerd image store)
- Default output is an mtree spec (lightweight); use `--output_groups=+tarball` to get actual tar

### `oci_push`
- Executable target — must use `bazel run`, NOT `bazel build`
- `repository`: can be omitted from BUILD and passed at runtime via `--repository` flag
- `remote_tags`: list of strings or label to a tags file
- Uses crane internally; auth from Docker config

### `tar` (from `@aspect_bazel_lib//lib:tar.bzl`)
- This is how you create layers for `oci_image`
- Simple usage: `tar(name = "layer", srcs = [":binary"])`
- For more control: `tar(name = "layer", srcs = [...], package_dir = "/usr/local/bin")`
  — `package_dir` places files under a specific directory in the layer
