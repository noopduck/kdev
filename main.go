// kdev - lightweight CLI to manage devcontainer pods in Kubernetes.
// Uses Kubernetes API directly to manage pods with PVC + RBAC-friendly SA.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/homedir"
	ptr "k8s.io/utils/pointer"
)

var (
	flagNamespace string
	kubeClient    *kubernetes.Clientset
)

func initKubeClient() error {
	// Use the current context from kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", filepath.Join(homedir.HomeDir(), ".kube", "config"))
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	kubeClient = clientset
	return nil
}

func main() {
	root := &cobra.Command{
		Use:   "kdev",
		Short: "Spin up, attach to, and clean up dev pods in Kubernetes",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if flagNamespace == "" {
				flagNamespace = "dev"
			}
			if err := initKubeClient(); err != nil {
				return fmt.Errorf("failed to initialize kubernetes client: %w", err)
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
		name         string
		template     string
		image        string
		sa           string
		pvc          string
		workdir      string
		labels       []string
		envs         []string
		cpu          string
		memory       string
		nodeSel      []string
		shell        string
		storageClass string
		storageSize  string
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
			if shell == "" {
				shell = "/bin/bash"
			}
			if storageClass == "" {
				storageClass = "local-path"
			}
			if storageSize == "" {
				storageSize = "20Gi"
			}

			ctx := context.Background()

			// Create PVC
			pvcSpec := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvc,
					Namespace: flagNamespace,
					Labels: map[string]string{
						"app":       "kdev",
						"kdev/name": name,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse(storageSize),
						},
					},
					StorageClassName: &storageClass,
					VolumeMode:       &[]corev1.PersistentVolumeMode{corev1.PersistentVolumeFilesystem}[0],
				},
			}

			// Create or update PVC
			_, err := kubeClient.CoreV1().PersistentVolumeClaims(flagNamespace).Create(ctx, pvcSpec, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create PVC: %w", err)
			}

			// Create ServiceAccount if it doesn't exist
			saSpec := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sa,
					Namespace: flagNamespace,
				},
			}

			_, err = kubeClient.CoreV1().ServiceAccounts(flagNamespace).Create(ctx, saSpec, metav1.CreateOptions{})
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("failed to create ServiceAccount: %w", err)
			}

			// Create Pod
			podLabels := map[string]string{
				"app":       "kdev",
				"kdev/name": name,
			}

			// Add custom labels
			for _, label := range labels {
				parts := strings.SplitN(label, "=", 2)
				if len(parts) == 2 {
					podLabels[parts[0]] = parts[1]
				}
			}

			// Parse node selector
			nodeSelector := make(map[string]string)
			for _, sel := range nodeSel {
				parts := strings.SplitN(sel, "=", 2)
				if len(parts) == 2 {
					nodeSelector[parts[0]] = parts[1]
				}
			}

			// Parse environment variables
			var envVars []corev1.EnvVar
			for _, env := range envs {
				parts := strings.SplitN(env, "=", 2)
				if len(parts) == 2 {
					envVars = append(envVars, corev1.EnvVar{
						Name:  parts[0],
						Value: parts[1],
					})
				}
			}

			// Create resource requirements if specified
			resources := corev1.ResourceRequirements{}
			if cpu != "" || memory != "" {
				resources.Requests = make(corev1.ResourceList)
				resources.Limits = make(corev1.ResourceList)

				if cpu != "" {
					cpuResource := resource.MustParse(cpu)
					resources.Requests[corev1.ResourceCPU] = cpuResource
					resources.Limits[corev1.ResourceCPU] = cpuResource
				}
				if memory != "" {
					memResource := resource.MustParse(memory)
					resources.Requests[corev1.ResourceMemory] = memResource
					resources.Limits[corev1.ResourceMemory] = memResource
				}
			}

			podSpec := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: flagNamespace,
					Labels:    podLabels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: sa,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:  ptr.Int64(1000),
						RunAsGroup: ptr.Int64(1000),
						FSGroup:    ptr.Int64(1000),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					NodeSelector: nodeSelector,
					Containers: []corev1.Container{{
						Name:       "dev",
						Image:      image,
						WorkingDir: workdir,
						Command:    []string{shell, "-lc", "while true; do sleep 3600; done"},
						Env:        envVars,
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             ptr.Bool(true),
							AllowPrivilegeEscalation: ptr.Bool(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
							ReadOnlyRootFilesystem: ptr.Bool(false),
						},
						Resources: resources,
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "work",
							MountPath: workdir,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "work",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvc,
							},
						},
					}},
				},
			}

			// Create Pod
			_, err = kubeClient.CoreV1().Pods(flagNamespace).Create(ctx, podSpec, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create Pod: %w", err)
			}

			fmt.Printf("\nPod %s created in ns/%s. Use 'kdev attach %s -n %s' to enter.\n", name, flagNamespace, name, flagNamespace)
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
	c.Flags().StringVar(&storageClass, "storage-class", "", "StorageClass for the PVC (default local-path)")
	c.Flags().StringVar(&storageSize, "storage", "", "PVC storage size (default 20Gi)")

	_ = c.MarkFlagRequired("name")
	_ = c.MarkFlagRequired("image")
	return c
}

func cmdAttach() *cobra.Command {
	var (
		name  string
		shell string
	)

	c := &cobra.Command{
		Use:   "attach",
		Short: "Attach an interactive shell to the dev pod",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			if shell == "" {
				shell = "/bin/bash"
			}
			req := kubeClient.CoreV1().RESTClient().Post().
				Resource("pods").
				Name(name).
				Namespace(flagNamespace).
				SubResource("exec").
				VersionedParams(&corev1.PodExecOptions{
					Command: []string{shell},
					Stdin:   true,
					Stdout:  true,
					Stderr:  true,
					TTY:     true,
				}, metav1.ParameterCodec)

			config, err := clientcmd.BuildConfigFromFlags("", filepath.Join(homedir.HomeDir(), ".kube", "config"))
			if err != nil {
				return fmt.Errorf("failed to get kubeconfig: %w", err)
			}

			exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
			if err != nil {
				return fmt.Errorf("failed to create executor: %w", err)
			}

			return exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
				Stderr: os.Stderr,
				Tty:    true,
			})
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

			pods, err := kubeClient.CoreV1().Pods(flagNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: "app=kdev",
			})
			if err != nil {
				return fmt.Errorf("failed to list pods: %w", err)
			}

			if len(pods.Items) == 0 {
				fmt.Println("No pods found")
				return nil
			}

			fmt.Printf("%-30s %-15s %-10s %-20s %-15s\n", "NAME", "READY", "STATUS", "NODE", "AGE")
			for _, pod := range pods.Items {
				ready := 0
				for _, c := range pod.Status.ContainerStatuses {
					if c.Ready {
						ready++
					}
				}
				age := time.Since(pod.CreationTimestamp.Time).Round(time.Second)
				fmt.Printf("%-30s %d/%-13d %-10s %-20s %-15s\n",
					pod.Name,
					ready,
					len(pod.Spec.Containers),
					pod.Status.Phase,
					pod.Spec.NodeName,
					age.String())
			}
			return nil
		},
	}
	return c
}

func cmdRM() *cobra.Command {
	var (
		name      string
		deletePVC bool
	)

	c := &cobra.Command{
		Use:   "rm",
		Short: "Delete a dev pod (optionally its PVC)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}

			ctx := context.Background()

			if err := kubeClient.CoreV1().Pods(flagNamespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
				return fmt.Errorf("failed to delete pod: %w", err)
			}

			if deletePVC {
				if err := kubeClient.CoreV1().PersistentVolumeClaims(flagNamespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
					return fmt.Errorf("failed to delete PVC: %w", err)
				}
			}

			fmt.Printf("Pod %s deleted in namespace %s\n", name, flagNamespace)
			if deletePVC {
				fmt.Printf("PVC %s deleted in namespace %s\n", name, flagNamespace)
			}
			return nil
		},
	}

	c.Flags().StringVar(&name, "name", "", "Pod name (required)")
	c.Flags().BoolVar(&deletePVC, "with-pvc", false, "Also delete PVC named like the pod")
	_ = c.MarkFlagRequired("name")
	return c
}
