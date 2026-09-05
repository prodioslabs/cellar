<p align="center">
  <img src="assets/cellar-logo.png" alt="Cellar — isometric C of slate-blue cubes with a single orange cube in the center" width="220" />
</p>

# Cellar

Cellar is a control plane for isolated [microsandbox](https://github.com/superradcompany/microsandbox) VMs. Run them on machines you already have — a laptop, a rack, or VMs on AWS or GCP.

Each sandbox is a real virtual machine, not a container. You install Cellar, start a cluster, and create sandboxes with the CLI or from your apps using the **official microsandbox SDKs**. Point the SDK at your Cellar gateway and pass an API key. There is no Cellar SDK.

**[Documentation](https://cellar.prodioslabs.in)** — install, architecture, sandboxes, and the client API.

## Install

```bash
curl -fsSL https://cellar.prodioslabs.in/install.sh | sh
```

Linux and macOS, amd64 and arm64. The installer places `cellard` (node daemon), `cellar` (CLI), and `cellar-gateway` (HTTP front door for apps). See the [install guide](https://cellar.prodioslabs.in/docs/install) for details.

## Quick start

Stand up a cluster and join a worker: **[Quick start](https://cellar.prodioslabs.in/docs/quick-start)**.

## License

MIT — see [LICENSE](LICENSE).
