package resolve

import (
	"context"
	"fmt"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StorageLocation resolves a BackupStorageLocation by name or default.
func StorageLocation(ctx context.Context, c client.Client, name string) (*backupv1alpha1.BackupStorageLocation, error) {
	if name != "" {
		var location backupv1alpha1.BackupStorageLocation
		if err := c.Get(ctx, types.NamespacedName{Name: name}, &location); err != nil {
			return nil, err
		}
		return &location, nil
	}

	var list backupv1alpha1.BackupStorageLocationList
	if err := c.List(ctx, &list); err != nil {
		return nil, err
	}

	if len(list.Items) == 0 {
		return nil, fmt.Errorf("no BackupStorageLocation found")
	}

	for i := range list.Items {
		if list.Items[i].Spec.Default {
			return &list.Items[i], nil
		}
	}

	if len(list.Items) == 1 {
		return &list.Items[0], nil
	}

	return nil, fmt.Errorf("no default BackupStorageLocation set")
}
