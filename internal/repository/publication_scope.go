package repository

import "fmt"

// Audience targeting is always evaluated inside the author's company. NULL
// memberships retain the legacy audience only for other unassigned users.
// SQL expressions are repository-owned column names or bound placeholders.
func publicationOrganizationScope(authorID, readerID string, allowPlatformAdmin bool) string {
	platformAccess := ""
	if allowPlatformAdmin {
		platformAccess = "(publication_reader.role IN ('admin', 'superadmin') AND publication_reader.organization_id IS NULL) OR "
	}
	return fmt.Sprintf(`EXISTS (
		SELECT 1 FROM users publication_author
		JOIN users publication_reader ON publication_reader.id = %s
		WHERE publication_author.id = %s
		  AND (%spublication_author.organization_id IS NOT DISTINCT FROM publication_reader.organization_id)
	)`, readerID, authorID, platformAccess)
}
