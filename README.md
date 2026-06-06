# tiny.haist.farm — URL shortener

A simple, self-hosted URL shortener deployed at https://tiny.haist.farm (OPS-1210).

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | HTML form — paste a long URL and optionally a custom code |
| `POST` | `/api/shorten` | Shorten a URL. Form fields: `url` (required), `code` (optional custom slug). Returns HTML or JSON (send `Accept: application/json` or `?format=json`). |
| `GET` | `/{code}` | 302 redirect to the original URL |
| `GET` | `/healthz` | Health probe — returns `ok` if the database is reachable |

## Security

Only `http://` and `https://` targets are accepted. `javascript:`, `data:`, `file:`,
and all other schemes are rejected with HTTP 400. This prevents the shortener from
being used as an XSS or open-redirect vector.

## Storage

SQLite database stored on a PersistentVolumeClaim at `/data/urls.db` via NFS storage
class. The deployment uses `strategy: Recreate` (replicas: 1) to ensure a single writer
at all times — SQLite over NFS is not safe with concurrent writers.

## CI / Supply-chain

Images are built by Forgejo Actions on push to `main`, cosign-signed with a Sigstore
key stored in Vault (`secret/cosign`), and attested with an SBOM and SLSA provenance
statement. The CI pipeline also runs gitleaks (secrets), OSV-scanner (Go dependencies),
and Trivy (container misconfig/CVEs) before pushing.

On successful push, CI digest-pins the image in
`overwatch-gitops/apps/tiny-shortener/deployment.yaml` as
`harbor.208.haist.farm/sentinel/tiny-shortener@sha256:<digest>`.

Deployment is managed by ArgoCD (automated sync, self-heal, prune).

<!-- ci re-trigger after forgejo regenerate hooks (OPS-1210); prior build-and-push fast-failed on "head commit missing in event payload" post-transfer -->
