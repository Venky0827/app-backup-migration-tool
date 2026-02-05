# Testing and Feature Exploration

This document describes how to validate the current scaffold and explore planned features.

Important notes
- Backups and restores run as Kubernetes Jobs using the same operator image.
- These steps validate CRDs, RBAC, storage wiring, and job execution.

## Prerequisites
- OpenShift cluster(s) or Kubernetes clusters with cluster-admin access.
- `oc` or `kubectl` configured for each cluster.
- Container build tool (`podman` or `docker`) if you will run the manager in-cluster.
- Go toolchain and Operator SDK if you will run the manager locally.
- Internet access for `go mod tidy` to pull dependencies (AWS SDK, client-go extras).

## Install CRDs and Controller

Option 1: Run locally against a cluster
1. Install CRDs:

```sh
make install
```

If `make install` is unavailable (missing controller-gen), apply the CRDs directly:

```sh
oc apply -f config/crd/bases
```

2. Run the controller locally:

```sh
make run
```

When running locally, set `OPERATOR_IMAGE` so jobs can launch with a valid image:
```sh
OPERATOR_IMAGE=<registry>/backup-operator:dev CLUSTER_ID=cluster-a make run
```

Option 2: Build and deploy to a cluster
1. Build and push the image:

```sh
make docker-build docker-push IMG=<registry>/backup-operator:dev
```

2. Deploy the operator:

```sh
make deploy IMG=<registry>/backup-operator:dev
```

3. Set environment variables (recommended):
- `OPERATOR_IMAGE` to match the deployed image
- `CLUSTER_ID` to a stable identifier (e.g., `cluster-a`)

You can patch the deployment if needed:

```sh
oc -n backup-operator-system set env deploy/backup-operator-controller-manager OPERATOR_IMAGE=<registry>/backup-operator:dev CLUSTER_ID=cluster-a
```

3. Verify the manager is running:

```sh
oc -n backup-operator-system get deploy
```

## Verify CRDs

```sh
oc get crds | grep backup.example.com
```

You should see:
- `backupstoragelocations.backup.example.com`
- `backups.backup.example.com`
- `clusterbackups.backup.example.com`
- `restores.backup.example.com`
- `clusterrestores.backup.example.com`
- `remoteclusters.backup.example.com`

## Create Storage Locations

S3 example:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: s3-creds
  namespace: backup-operator-system
stringData:
  accessKey: <access-key>
  secretKey: <secret-key>
  sessionToken: <optional-session-token>
---
apiVersion: backup.example.com/v1alpha1
kind: BackupStorageLocation
metadata:
  name: primary-s3
spec:
  type: s3
  s3:
    endpoint: https://s3.example.com
    bucket: backups
    prefix: demo
    secretRef:
      name: s3-creds
      namespace: backup-operator-system
```

NFS example:
```yaml
apiVersion: backup.example.com/v1alpha1
kind: BackupStorageLocation
metadata:
  name: primary-nfs
spec:
  type: nfs
  nfs:
    server: 10.0.0.10
    path: /exports/backups
```

Note: For cross-cluster restores, both clusters must mount the same NFS export.
If your NFS requires authentication, use a CSI/PVC-backed NFS setup and provide credentials via the storage class.

Apply:
```sh
oc apply -f <file>.yaml
```

Check status and artifact location:
```sh
oc -n app1 get backup app1-backup -o yaml
```

## Create Remote Cluster References

On each cluster, create a `RemoteCluster` that points to the other cluster.

Get a token for the operator service account (created on install):

```sh
oc -n backup-operator-system create token backup-operator-controller-manager
```

Example Secret (expected keys):
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: remote-cluster-auth
  namespace: backup-operator-system
stringData:
  token: <service-account-token>
  ca.crt: <base64-encoded-ca>
```

Example `RemoteCluster`:
```yaml
apiVersion: backup.example.com/v1alpha1
kind: RemoteCluster
metadata:
  name: peer-cluster
spec:
  clusterID: cluster-b
  apiServer: https://api.cluster-b.example.com:6443
  auth:
    method: ServiceAccountToken
    secretRef:
      name: remote-cluster-auth
      namespace: backup-operator-system
```

## Create Backup Requests

Namespace backup:
```yaml
apiVersion: backup.example.com/v1alpha1
kind: Backup
metadata:
  name: app1-backup
  namespace: app1
spec:
  storageRef:
    name: primary-s3
  export:
    enabled: true
    format: yaml
  snapshot:
    enabled: true
    includeAllPVCs: true
```

Note: VolumeSnapshot CRDs and a CSI snapshot class must exist for snapshots to be created.

Cluster backup:
```yaml
apiVersion: backup.example.com/v1alpha1
kind: ClusterBackup
metadata:
  name: full-backup
spec:
  storageRef:
    name: primary-s3
  includeClusterResources: true
  namespaces:
    included:
      - app1
      - app2
  export:
    enabled: true
  snapshot:
    enabled: true
```

## Create Restore Requests

Namespace restore (from namespace backup):
```yaml
apiVersion: backup.example.com/v1alpha1
kind: Restore
metadata:
  name: app1-restore
  namespace: app1
spec:
  sourceRef:
    kind: Backup
    name: app1-backup
```

Cluster restore (from cluster backup):
```yaml
apiVersion: backup.example.com/v1alpha1
kind: ClusterRestore
metadata:
  name: full-restore
spec:
  sourceRef:
    kind: ClusterBackup
    name: full-backup
```

Restore to remote cluster:
```yaml
apiVersion: backup.example.com/v1alpha1
kind: Restore
metadata:
  name: app1-restore-remote
  namespace: app1
spec:
  sourceRef:
    kind: Backup
    name: app1-backup
  targetClusterRef:
    name: peer-cluster
```

Note: Create the remote restore in the same cluster where the source Backup exists.

## Observe Reconcile Events

Since logic is not implemented yet, use logs to confirm reconcile triggers:
```sh
oc -n backup-operator-system logs deploy/backup-operator-controller-manager -c manager
```

You should see log lines for Backup, ClusterBackup, Restore, ClusterRestore, and RemoteCluster reconciles.

## Job Execution
Each Backup and Restore creates a Kubernetes Job in `backup-operator-system`.

```sh
oc -n backup-operator-system get jobs
```

The Job runs the same operator image in worker mode and updates status when complete.

To inspect a job's logs:
```sh
oc -n backup-operator-system logs job/<job-name> -c backup-worker
```

## Cleanup

```sh
make undeploy
make uninstall
```
