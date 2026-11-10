package domain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These checks read the consumers' real permission registries. They need no
// network or database; provide checkouts through the documented environment
// variables when auditing a new version of a consumer.
func TestAccessCatalogODOServiceContract(t *testing.T) {
	repo := accessContractRepo(t, "ACCESS_CONTRACT_ODO_REPO", "go.mod")
	files, err := filepath.Glob(filepath.Join(repo, "internal", "migrations", "sql", "auth", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("ODO auth migrations not found: %v", err)
	}
	registered := map[string]bool{}
	codePattern := regexp.MustCompile(`'((?:npa\.)?[a-z_]+\.[a-z_]+)'`)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.SplitN(string(data), "-- +goose Down", 2)[0]
		for _, match := range codePattern.FindAllStringSubmatch(up, -1) {
			registered[match[1]] = true
		}
	}
	excluded := map[string]bool{
		"user.manage": true, "department.manage": true, "position.manage": true,
		"permission.manage": true, "dictionary.manage": true, "delegation.manage": true,
		"audit.read": true,
	}
	assertAccessRegistry(t, AccessCatalog()["odo"], registered, excluded)
}

func TestAccessCatalogSDServiceContract(t *testing.T) {
	repo := accessContractRepo(t, "ACCESS_CONTRACT_SD_REPO", "go.mod")
	model := parseAccessContract(t, filepath.Join(repo, "internal", "domain", "models.go"))
	constants := accessContractConstants(t, model)
	registry := parseAccessContract(t, filepath.Join(repo, "internal", "repository", "postgres", "rbac_repository.go"))
	definitions := accessContractMap(t, registry, "permissionDefinitions")
	registered := map[string]bool{}
	for _, element := range definitions.Elts {
		resource := element.(*ast.KeyValueExpr)
		for _, action := range resource.Value.(*ast.CompositeLit).Elts {
			code := accessContractValue(t, resource.Key, constants) + ":" + accessContractValue(t, action.(*ast.KeyValueExpr).Key, constants)
			registered[code] = true
		}
	}
	assertAccessRegistry(t, AccessCatalog()["sd"], registered, map[string]bool{"system_audit:view_audit_all_system": true})

	// Client companies must get exactly the allowlist enforced by SenimDesk,
	// not an independently broadened copy of the staff catalog.
	client := parseAccessContract(t, filepath.Join(repo, "internal", "domain", "rbac_client_permissions.go"))
	allActions := []string{}
	for _, declaration := range client.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "allPermissionActions" {
			continue
		}
		values := function.Body.List[0].(*ast.ReturnStmt).Results[0].(*ast.CompositeLit)
		for _, value := range values.Elts {
			allActions = append(allActions, accessContractValue(t, value.(*ast.KeyValueExpr).Key, constants))
		}
	}
	if len(allActions) == 0 {
		t.Fatal("SenimDesk client action definitions not found")
	}
	allowed := map[string]bool{}
	for _, element := range accessContractMap(t, client, "clientPermissionAllowlist").Elts {
		resource := element.(*ast.KeyValueExpr)
		call := resource.Value.(*ast.CallExpr)
		actions := []string{}
		switch call.Fun.(*ast.Ident).Name {
		case "allPermissionActions":
			actions = allActions
		case "permissionActions":
			for _, action := range call.Args {
				actions = append(actions, accessContractValue(t, action, constants))
			}
		default:
			t.Fatal("unknown client permission definition; review the upstream contract")
		}
		for _, action := range actions {
			allowed[accessContractValue(t, resource.Key, constants)+":"+action] = true
		}
	}
	got := map[string]bool{}
	for _, permission := range SDClientPermissions(AccessCatalog()["sd"]) {
		got[permission.Code] = true
	}
	for _, permission := range AccessCatalog()["sd"] {
		if got[permission.Code] != allowed[permission.Code] {
			t.Errorf("client permission %s: Workspace=%v, SenimDesk=%v", permission.Code, got[permission.Code], allowed[permission.Code])
		}
	}
	for code := range allowed {
		if !got[code] {
			t.Errorf("SenimDesk client permission absent from Workspace: %s", code)
		}
	}
}

func TestAccessCatalogIFormServiceContract(t *testing.T) {
	repo := accessContractRepo(t, "ACCESS_CONTRACT_IFORM_REPO", filepath.Join("backend", "pom.xml"))
	data, err := os.ReadFile(filepath.Join(repo, "backend", "src", "main", "java", "kz", "beles", "iform", "identity", "WorkspaceDirectory.java"))
	if err != nil {
		t.Fatal(err)
	}
	registry := regexp.MustCompile(`(?s)public\s+static\s+final\s+Set<String>\s+ROLES\s*=\s*Set\.of\(([^;]+)\);`).FindStringSubmatch(string(data))
	if len(registry) != 2 {
		t.Fatal("iForm Workspace role registry changed; review its contract")
	}
	registered := map[string]bool{}
	for _, match := range regexp.MustCompile(`"([A-Z_]+)"`).FindAllStringSubmatch(registry[1], -1) {
		registered[match[1]] = true
	}
	roles := make([]AccessPermission, 0, len(IFormPermissionRoles))
	for _, permission := range AccessCatalog()["iform"] {
		role, ok := IFormPermissionRoles[permission.Code]
		if !ok {
			t.Fatalf("iForm permission has no role mapping: %s", permission.Code)
		}
		roles = append(roles, AccessPermission{Code: role})
	}
	// CLIENT belongs to a different audience; TENANT_ADMIN is not a job grant.
	// The current owner's implicit roles are a separate, documented legacy gap.
	assertAccessRegistry(t, roles, registered, map[string]bool{"TENANT_ADMIN": true, "CLIENT": true})
}

func accessContractRepo(t *testing.T, variable, marker string) string {
	t.Helper()
	path := os.Getenv(variable)
	if path == "" {
		t.Skipf("set %s to a consumer checkout; see docs/access-permission-catalog.md", variable)
	}
	if info, err := os.Stat(filepath.Join(path, marker)); err != nil || info.IsDir() {
		t.Fatalf("invalid consumer checkout %s", variable)
	}
	return path
}

func assertAccessRegistry(t *testing.T, catalog []AccessPermission, registered, excluded map[string]bool) {
	t.Helper()
	delegated := map[string]bool{}
	for _, permission := range catalog {
		if !registered[permission.Code] {
			t.Errorf("Workspace offers unregistered consumer permission: %s", permission.Code)
		}
		if excluded[permission.Code] {
			t.Errorf("system permission is delegated: %s", permission.Code)
		}
		if delegated[permission.Code] {
			t.Errorf("duplicate permission: %s", permission.Code)
		}
		delegated[permission.Code] = true
	}
	for code := range registered {
		if !delegated[code] && !excluded[code] {
			t.Errorf("consumer permission has no delegation decision: %s", code)
		}
	}
}

func parseAccessContract(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("cannot parse consumer contract %s: %v", path, err)
	}
	return file
}

func accessContractMap(t *testing.T, file *ast.File, name string) *ast.CompositeLit {
	t.Helper()
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if ok && len(value.Names) == 1 && value.Names[0].Name == name && len(value.Values) == 1 {
				if literal, ok := value.Values[0].(*ast.CompositeLit); ok {
					return literal
				}
			}
		}
	}
	t.Fatalf("consumer definition %s changed; review its contract", name)
	return nil
}

func accessContractConstants(t *testing.T, file *ast.File) map[string]string {
	t.Helper()
	values := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
			return true
		}
		if literal, ok := value.Values[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			values[value.Names[0].Name] = text
		}
		return true
	})
	return values
}

func accessContractValue(t *testing.T, expr ast.Expr, constants map[string]string) string {
	t.Helper()
	name := ""
	switch value := expr.(type) {
	case *ast.SelectorExpr:
		name = value.Sel.Name
	case *ast.Ident:
		name = value.Name
	}
	if text, ok := constants[name]; ok {
		return text
	}
	t.Fatalf("unknown consumer constant %s; review the upstream contract", name)
	return ""
}
