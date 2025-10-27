package devcontainer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Minimal struktur av devcontainer.json
type DevContainerConfig struct {
	Name  string `json:"name"`
	Build struct {
		Dockerfile string            `json:"dockerfile"`
		Context    string            `json:"context"`
		Args       map[string]string `json:"args"`
	} `json:"build"`
}

// Ny kommando: `kdev devcontainer build`
func cmdDevContainer() *cobra.Command {
	var (
		push      bool
		imageName string
	)

	c := &cobra.Command{
		Use:   "devcontainer",
		Short: "Handle devcontainer builds",
	}

	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build a .devcontainer image based on devcontainer.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := readDevContainerConfig(".devcontainer/devcontainer.json")
			if err != nil {
				return err
			}

			// Default fallbacks
			if cfg.Build.Dockerfile == "" {
				cfg.Build.Dockerfile = "Dockerfile"
			}
			if cfg.Build.Context == "" {
				cfg.Build.Context = "."
			}

			if imageName == "" {
				imageName = fmt.Sprintf("harbor.intern.k8s.darkworks.no/dev/%s:latest", cfg.Name)
			}

			dockerfile := filepath.Join(".devcontainer", cfg.Build.Dockerfile)
			context := cfg.Build.Context

			// Build args
			var buildArgs []string
			for k, v := range cfg.Build.Args {
				buildArgs = append(buildArgs, "--build-arg", fmt.Sprintf("%s=%s", k, v))
			}

			argsList := append([]string{"build", "-f", dockerfile, "-t", imageName}, buildArgs...)
			argsList = append(argsList, context)

			fmt.Printf("🚧 Building %s from %s\n", imageName, dockerfile)
			build := exec.Command("docker", argsList...)
			build.Stdout = os.Stdout
			build.Stderr = os.Stderr
			if err := build.Run(); err != nil {
				return fmt.Errorf("docker build failed: %w", err)
			}

			if push {
				fmt.Printf("📦 Pushing %s...\n", imageName)
				pushCmd := exec.Command("docker", "push", imageName)
				pushCmd.Stdout = os.Stdout
				pushCmd.Stderr = os.Stderr
				if err := pushCmd.Run(); err != nil {
					return fmt.Errorf("docker push failed: %w", err)
				}
			}

			fmt.Printf("✅ Devcontainer image ready: %s\n", imageName)
			return nil
		},
	}

	buildCmd.Flags().BoolVar(&push, "push", false, "Push the built image to registry")
	buildCmd.Flags().StringVar(&imageName, "image", "", "Override image name (default derived from devcontainer.json name)")
	c.AddCommand(buildCmd)

	return c
}

func readDevContainerConfig(path string) (*DevContainerConfig, error) {
	f, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var cfg DevContainerConfig
	if err := json.Unmarshal(f, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if cfg.Name == "" {
		cfg.Name = "devcontainer"
	}
	return &cfg, nil
}
