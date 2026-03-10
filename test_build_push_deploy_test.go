package test_build_push_deploy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/unbiasedzucchini/test-build-push-deploy/deploy"
)

func TestBuildPushDeploy(t *testing.T) {
	ctx := context.Background()

	// ── Step 1: Build the sample Go binary ─────────────────────────
	t.Log("Step 1: Building sample Go binary")

	// Bazel sets TEST_SRCDIR and RUNFILES for test data.
	// We pass the sample app source as data and build it with `go build`.
	sampleAppDir, err := findRunfile("testdata/sample_app")
	if err != nil {
		t.Fatalf("finding sample_app dir: %v", err)
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "sample_app")

	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = sampleAppDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building sample app: %v\n%s", err, out)
	}

	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}
	t.Logf("Built binary: %s (%d bytes)", binaryPath, info.Size())

	// ── Step 2: Build a container image ────────────────────────────
	t.Log("Step 2: Building container image")

	// Create a tarball layer containing the binary at /app/sample_app
	layerTarPath := filepath.Join(tmpDir, "layer.tar")
	if err := createLayerTar(layerTarPath, binaryPath, "app/sample_app"); err != nil {
		t.Fatalf("creating layer tar: %v", err)
	}

	layer, err := tarball.LayerFromFile(layerTarPath)
	if err != nil {
		t.Fatalf("creating layer: %v", err)
	}

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("appending layer: %v", err)
	}

	img, err = mutate.Config(img, v1Config("/app/sample_app"))
	if err != nil {
		t.Fatalf("setting config: %v", err)
	}

	// ── Step 3: Push to a local registry ───────────────────────────
	t.Log("Step 3: Starting local registry and pushing image")

	reg := registry.New()
	server := httptest.NewServer(reg)
	defer server.Close()

	registryAddr := strings.TrimPrefix(server.URL, "http://")
	imageRef := fmt.Sprintf("%s/sample-app:test", registryAddr)

	if err := crane.Push(img, imageRef); err != nil {
		t.Fatalf("pushing image: %v", err)
	}

	// Verify image is in the registry
	digest, err := crane.Digest(imageRef)
	if err != nil {
		t.Fatalf("getting digest: %v", err)
	}
	t.Logf("Pushed image %s with digest %s", imageRef, digest)

	// ── Step 4: Deploy to fake k8s and verify ──────────────────────
	t.Log("Step 4: Deploying to Kubernetes (fake client)")

	k8sClient := fake.NewSimpleClientset()

	cfg := deploy.Config{
		Name:      "sample-app",
		Namespace: "default",
		Image:     imageRef,
		Port:      8080,
		Replicas:  2,
	}

	if err := deploy.Apply(ctx, k8sClient, cfg); err != nil {
		t.Fatalf("deploying: %v", err)
	}

	// Verify Deployment
	dep, err := k8sClient.AppsV1().Deployments("default").Get(ctx, "sample-app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}

	if got := *dep.Spec.Replicas; got != 2 {
		t.Errorf("replicas = %d, want 2", got)
	}

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("container count = %d, want 1", len(containers))
	}
	if got := containers[0].Image; got != imageRef {
		t.Errorf("image = %q, want %q", got, imageRef)
	}
	if got := containers[0].Ports[0].ContainerPort; got != 8080 {
		t.Errorf("containerPort = %d, want 8080", got)
	}

	// Verify Service
	svc, err := k8sClient.CoreV1().Services("default").Get(ctx, "sample-app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting service: %v", err)
	}
	if got := svc.Spec.Ports[0].Port; got != 8080 {
		t.Errorf("service port = %d, want 8080", got)
	}
	if got := svc.Spec.Selector["app"]; got != "sample-app" {
		t.Errorf("service selector = %q, want %q", got, "sample-app")
	}

	t.Log("All steps passed: build → push → deploy ✓")
}

// findRunfile locates a runfile path, supporting both Bazel and direct `go test`.
func findRunfile(relPath string) (string, error) {
	// When run under Bazel, check RUNFILES_DIR
	if rf := os.Getenv("RUNFILES_DIR"); rf != "" {
		// The workspace name is the module name
		candidate := filepath.Join(rf, "_main", relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// Also check TEST_SRCDIR
	if ts := os.Getenv("TEST_SRCDIR"); ts != "" {
		candidate := filepath.Join(ts, "_main", relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// Fallback: relative to working directory (for `go test`)
	abs, err := filepath.Abs(relPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("cannot find %s (tried runfiles and cwd): %w", relPath, err)
	}
	return abs, nil
}
