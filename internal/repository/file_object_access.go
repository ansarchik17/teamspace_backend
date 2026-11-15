package repository

import "context"

// IsManagedFileObject includes trashed files and historical versions: physical
// deletion through a generic upload endpoint would bypass resource permissions
// and destroy the ability to restore them. Note audio also has a dedicated ACL.
func (r *DriveRepository) IsManagedFileObject(ctx context.Context, objectName string) (bool, error) {
	var managed bool
	err := r.db.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM files WHERE object_name=$1) OR
		EXISTS(SELECT 1 FROM file_versions WHERE object_name=$1) OR
		EXISTS(SELECT 1 FROM note_audio_attachments WHERE storage_key=$1)`, objectName).Scan(&managed)
	return managed, err
}
