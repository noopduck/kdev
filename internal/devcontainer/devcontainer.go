package devcontainer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

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
	Features       map[string]interface{} `json:"features,omitempty"`
	Customizations struct {
		VSCode struct {
			Extensions []string `json:"extensions,omitempty"`
		} `json:"vscode,omitempty"`
	} `json:"customizations,omitempty"`
	RemoteUser string `json:"remoteUser,omitempty"`
}

func sanitizeImageNamePart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile(`[^a-z0-9._-]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "devcontainer"
	}
	return s
}

// Ny kommando: `kdev devcontainer build`
func CmdDevContainer() *cobra.Command {
	var (
		push             bool
		imageName        string
		registry         string
		tag              string
		platform         string
		useDevcontainers bool
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

			// If features are present and user requested it, use the devcontainers CLI to build
			if len(cfg.Features) > 0 && useDevcontainers {
				if imageName == "" {
					if registry == "" || tag == "" {
						return fmt.Errorf("either --image or both --registry and --tag must be provided (required when using devcontainers CLI)")
					}
					imageName = fmt.Sprintf("%s/%s:%s", registry, sanitizeImageNamePart(cfg.Name), tag)
				}
				fmt.Printf("🚧 Building with devcontainers CLI (features detected)...\n")
				cmdArgs := []string{"build", "--workspace-folder", ".", "--image-name", imageName}
				if platform != "" {
					cmdArgs = append(cmdArgs, "--platform", platform)
				}
				dc := exec.Command("devcontainer", cmdArgs...)
				dc.Stdout = os.Stdout
				dc.Stderr = os.Stderr
				return dc.Run()
			}

			// Resolve image name:
			// - If --image provided, use that (may include registry and tag)
			// - Otherwise require both --registry and --tag
			if imageName == "" {
				if registry == "" || tag == "" {
					return fmt.Errorf("either --image or both --registry and --tag must be provided")
				}
				imageName = fmt.Sprintf("%s/%s:%s", registry, sanitizeImageNamePart(cfg.Name), tag)
			}

			dockerfile := filepath.Join(".devcontainer", cfg.Build.Dockerfile)
			context := cfg.Build.Context

			// validate dockerfile exists
			if _, err := os.Stat(dockerfile); err != nil {
				return fmt.Errorf("dockerfile not found: %s", dockerfile)
			}

			// Build args (handle nil map)
			var buildArgs []string
			if cfg.Build.Args != nil {
				for k, v := range cfg.Build.Args {
					buildArgs = append(buildArgs, "--build-arg", fmt.Sprintf("%s=%s", k, v))
				}
			}
			// pass remoteUser as build-arg so Dockerfile can use it if desired
			if cfg.RemoteUser != "" {
				buildArgs = append(buildArgs, "--build-arg", fmt.Sprintf("REMOTE_USER=%s", cfg.RemoteUser))
			}

			// Compose docker build args
			base := []string{"build"}
			if platform != "" {
				base = append(base, "--platform", platform)
			}
			base = append(base, "-f", dockerfile, "-t", imageName)
			argsList := append(base, buildArgs...)
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
	buildCmd.Flags().StringVar(&imageName, "image", "", "Override image name (can include registry and tag)")
	buildCmd.Flags().StringVar(&registry, "registry", "", "Container registry (e.g. harbor.example.com) — required if --image not set")
	buildCmd.Flags().StringVar(&tag, "tag", "", "Image tag (required if --image not set)")
	buildCmd.Flags().StringVar(&platform, "platform", "", "Target platform (e.g. linux/arm64)")
	buildCmd.Flags().BoolVar(&useDevcontainers, "use-devcontainers-cli", false, "If features are present, invoke the devcontainers CLI to build the image")
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
