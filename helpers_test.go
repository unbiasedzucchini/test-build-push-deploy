package test_build_push_deploy

import (
	"archive/tar"
	"io"
	"os"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// createLayerTar creates a tar archive with a single file.
func createLayerTar(tarPath, srcFile, destPath string) error {
	f, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	tw := tar.NewWriter(f)
	defer tw.Close()

	src, err := os.Open(srcFile)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}

	hdr := &tar.Header{
		Name: destPath,
		Mode: 0755,
		Size: info.Size(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, src)
	return err
}

// v1Config returns a v1.Config with the given entrypoint.
func v1Config(entrypoint string) v1.Config {
	return v1.Config{
		Entrypoint: []string{entrypoint},
	}
}
