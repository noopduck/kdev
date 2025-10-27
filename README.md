# kdev (MVP)

A lightweight Go-CLI that spins up, attaches to and cleans up **devpods** (dev container in Kubernetes).
It wraps arround `kubectl` and renders a simple Pod-template. RBAC, non-root, PVC - everything you enjoy. 😎

## Install
```bash
go install ./...
# eller
go build -o kdev
```

## Use
```bash
# Create devpod
./kdev up --name mydev --image registry.local/your/devimage:latest -n dev --env FOO=bar --cpu 1000m --memory 2Gi

# List dev pods
./kdev ls -n dev

# Attach
./kdev attach --name mydev -n dev

# Delete pod (Also remove the pvc as long as it's name is the same as the pods name)
./kdev rm --name mydev -n dev --with-pvc
```

## Devcontainer build

kdev supports building images from a `.devcontainer/devcontainer.json` file. The command requires either an explicit image name or both a registry and tag. Example:

```bash
# provide registry and tag (image will be <registry>/<name>:<tag>)
./kdev devcontainer build --registry harbor.example.com --tag v1.2.3 --push

# or provide a full image name (including registry and tag)
./kdev devcontainer build --image harbor.example.com/myproj/devcontainer:v1.2.3 --push
```

Flags:
- `--image` — override the full image name (can include registry and tag)
- `--registry` and `--tag` — used together to construct image name when `--image` is not provided
- `--push` — push the image after a successful build

Note about the devcontainers CLI and `npm`

If your `.devcontainer/devcontainer.json` uses `features` you likely want the official devcontainers CLI to build the image so features are applied correctly. The devcontainers CLI is distributed via npm (Node.js). That means you need `node`/`npm` available to install it with the standard npm workflow. Example install:

```bash
# install globally via npm (requires Node.js/npm on your machine)
npm install -g @devcontainers/cli

# then build with kdev using the CLI to apply features
./kdev devcontainer build --registry harbor.example.com --tag v1.2.3 --use-devcontainers-cli
```

If you don't want to install via npm you can use the install script from the project (see devcontainers CLI docs) or build the image yourself and provide the resulting image to `kdev` with `--image`.



## kubeconfig requirement

kdev uses the Kubernetes API (client-go) and therefore needs access to a kubeconfig file to talk to your cluster. By default it will look for kubeconfig at `~/.kube/config` using the normal kube rules.

If your kubeconfig is elsewhere, set the `KUBECONFIG` environment variable or point `kubectl`/client-go to a different file before running kdev. Example:

```bash
export KUBECONFIG=/path/to/your/kubeconfig
./kdev ls -n dev
```

Alternatively use your normal kubectl context configuration (the same kubeconfig locations are honored).

Note: Previously kdev wrapped `kubectl`; the current implementation uses the Kubernetes client library directly and needs a valid kubeconfig to authenticate and connect.


## Template-variable
`templates/pod.yaml` supports these placeholders:
`{{NAME}} {{NAMESPACE}} {{IMAGE}} {{SERVICE_ACCOUNT}} {{PVC_NAME}} {{WORKDIR}} {{CPU}} {{MEMORY}} {{SHELL}} {{LABELS_EXTRA}} {{ENVS}} {{NODE_SELECTOR}} {{STORAGE_CLASS}} {{STORAGE_SIZE}}`

