package repository

import "fmt"

func kanbanOrgScopeCondition(userAlias, deptAlias, divAlias, divDeptAlias, stateBodyExpr string) string {
	return "FALSE"
}

func kanbanLeadershipBoardAccessExists(boardExpr, userParam string) string {
	return "FALSE"
}

func kanbanExplicitBoardAccessExists(boardExpr, userParam string) string {
	return "FALSE"
}

func kanbanBoardVisibilityCondition(boardAlias, userParam string) string {
	return fmt.Sprintf(`%s.deleted_at IS NULL
	 AND COALESCE(to_jsonb(%s)->>'system_key','')<>'documentolog-assignments'
	 AND COALESCE(to_jsonb(%s)->>'system_key','') NOT LIKE 'documentolog-assignments:%%' AND (
		%s.owner_id = %s
		OR EXISTS (SELECT 1 FROM board_members bm WHERE bm.board_id = %s.id AND bm.user_id = %s)
	)`, boardAlias, boardAlias, boardAlias, boardAlias, userParam, boardAlias, userParam)
}
