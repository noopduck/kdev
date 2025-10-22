// kdev - lightweight CLI to manage devcontainer pods in Kubernetes.
// MVP: wraps kubectl and renders a simple Pod template with PVC + RBAC-friendly SA.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	flagNamespace string
)

func runKubectl(args ...string) error {
	cmd := exec.Command("kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runKubectlCapture(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	out := &bytes.Buffer{}
	errB := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = errB
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("kubectl %v failed: %w\n%s", strings.Join(args, " "), err, errB.String())
	}
	return out.String(), nil
}

func main() {
	root := &cobra.Command{
		Use:   "kdev",
		Short: "Spin up, attach to, and clean up dev pods in Kubernetes",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if flagNamespace == "" {
				flagNamespace = "dev"
			}
			return nil
		},
	}

	root.PersistentFlags().StringVarP(&flagNamespace, "namespace", "n", "dev", "Kubernetes namespace")

	root.AddCommand(cmdUp(), cmdAttach(), cmdLS(), cmdRM())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func cmdUp() *cobra.Command {
	var (
		name        string
		template    string
		image       string
		sa         string
		pvc         string
		workdir     string
		labels      []string
		envs        []string
		cpu         string
		memory      string
		nodeSel     []string
		shell       string
	)

	c := &cobra.Command{
		Use:   "up",
		Short: "Create (or update) a dev pod from a template",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			if image == "" {
				return errors.New("--image is required")
			}
			if sa == "" {
				sa = "dev-vscode"
			}
			if pvc == "" {
				pvc = name
			}
			if template == "" {
				template = filepath.Join("templates", "pod.yaml")
			}
			if shell == "" { shell = "/bin/bash" }

			b, err := ioutil.ReadFile(template)
			if err != nil {
				return err
			}
			rendered := string(b)

			// simple placeholder replacement

			repl := map[string]string{
				"{{NAME}}":      name,
				"{{NAMESPACE}}": flagNamespace,
				"{{IMAGE}}":     image,
				"{{SERVICE_ACCOUNT}}": sa,
				"{{PVC_NAME}}":  pvc,
				"{{WORKDIR}}":   workdir,
				"{{CPU}}":       cpu,
				"{{MEMORY}}":    memory,
				"{{SHELL}}":     shell,
			}

			for k, v := range repl {
				if v == "" {
					continue
				}
				rendered = strings.ReplaceAll(rendered, k, v)
			}

			// labels

			if len(labels) > 0 {
				lbl := make([]string, 0, len(labels))
				for _, kv := range labels { lbl = append(lbl, "  "+kv) }
				rendered = strings.ReplaceAll(rendered, "{{LABELS_EXTRA}}", strings.Join(lbl, "\n"))
			} else {
				rendered = strings.ReplaceAll(rendered, "{{LABELS_EXTRA}}", "")
			}

			// envs

			if len(envs) > 0 {
				var lines []string
				for _, kv := range envs { // KEY=VAL
					parts := strings.SplitN(kv, "=", 2)
					if len(parts) != 2 { continue }
					lines = append(lines, fmt.Sprintf("        - name: %s\n          value: \"%s\"", parts[0], strings.ReplaceAll(parts[1], "\"", "\\\"")))
				}
				rendered = strings.ReplaceAll(rendered, "{{ENVS}}", strings.Join(lines, "\n"))
			} else {
				rendered = strings.ReplaceAll(rendered, "{{ENVS}}", "")
			}

			// nodeSelector

			if len(nodeSel) > 0 {
				var lines []string
				for _, kv := range nodeSel { // key=val
					parts := strings.SplitN(kv, "=", 2)
					if len(parts) != 2 { continue }
					lines = append(lines, fmt.Sprintf("    %s: \"%s\"", parts[0], strings.ReplaceAll(parts[1], "\"", "\\\"")))
				}
				rendered = strings.ReplaceAll(rendered, "{{NODE_SELECTOR}}", strings.Join(lines, "\n"))
			} else {
				rendered = strings.ReplaceAll(rendered, "{{NODE_SELECTOR}}", "")
			}

			// apply via kubectl

			apply := exec.Command("kubectl", "apply", "-n", flagNamespace, "-f", "-")
			apply.Stdin = strings.NewReader(rendered)
			apply.Stdout = os.Stdout
			apply.Stderr = os.Stderr
			if err := apply.Run(); err != nil {
				return err
			}

			fmt.Printf("\nPod %s applied in ns/%s. Use 'kdev attach %s -n %s' to enter.\n", name, flagNamespace, name, flagNamespace)
			return nil
		},
	}

	c.Flags().StringVar(&name, "name", "", "Pod name (required)")
	c.Flags().StringVar(&template, "template", "", "Path to Pod template (default templates/pod.yaml)")
	c.Flags().StringVar(&image, "image", "", "Container image (required)")
	c.Flags().StringVar(&sa, "service-account", "", "ServiceAccount name (default dev-vscode)")
	c.Flags().StringVar(&pvc, "pvc", "", "PVC name to mount (default: same as name)")
	c.Flags().StringVar(&workdir, "workdir", "/workspaces", "Workspace directory inside container")
	c.Flags().StringSliceVar(&labels, "label", nil, "Extra labels key=value (repeatable)")
	c.Flags().StringSliceVar(&envs, "env", nil, "Env vars KEY=VALUE (repeatable)")
	c.Flags().StringVar(&cpu, "cpu", "", "CPU request/limit, e.g. 500m")
	c.Flags().StringVar(&memory, "memory", "", "Memory request/limit, e.g. 1Gi")
	c.Flags().StringSliceVar(&nodeSel, "node", nil, "Node selector key=value (repeatable)")
	c.Flags().StringVar(&shell, "shell", "", "Login shell inside container (default /bin/bash)")

	_ = c.MarkFlagRequired("name")
	_ = c.MarkFlagRequired("image")
	return c
}

func cmdAttach() *cobra.Command {
	var (
		name string
		shell string
	)

	c := &cobra.Command{
		Use:   "attach",
		Short: "Attach an interactive shell to the dev pod",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" { return errors.New("--name is required") }
			if shell == "" { shell = "/bin/bash" }
			return runKubectl("exec", "-n", flagNamespace, "-it", name, "--", shell)
		},
	}

	c.Flags().StringVar(&name, "name", "", "Pod name (required)")
	c.Flags().StringVar(&shell, "shell", "", "Shell to start inside container (default /bin/bash)")
	_ = c.MarkFlagRequired("name")
	return c
}

func cmdLS() *cobra.Command {
	c := &cobra.Command{
		Use:   "ls",
		Short: "List dev pods in the namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Namespace: %s\n", flagNamespace)
			return runKubectl("get", "pods", "-n", flagNamespace, "-l", "app=kdev", "-o", "wide")
		},
	}
	return c
}

func cmdRM() *cobra.Command {
	var (
		name string
		deletePVC bool
	)

	c := &cobra.Command{
		Use:   "rm",
		Short: "Delete a dev pod (optionally its PVC)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" { return errors.New("--name is required") }
			if err := runKubectl("delete", "pod", "-n", flagNamespace, name); err != nil {
				return err
			}
			if deletePVC {
				if err := runKubectl("delete", "pvc", "-n", flagNamespace, name); err != nil {
					return err
				}
			}
			return nil
		},
	}

	c.Flags().StringVar(&name, "name", "", "Pod name (required)")
	c.Flags().BoolVar(&deletePVC, "with-pvc", false, "Also delete PVC named like the pod")
	_ = c.MarkFlagRequired("name")
	return c
}


