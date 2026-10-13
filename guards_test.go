package caerus_test

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

var forbiddenImports = []string{"google.golang.org/grpc", "google.golang.org/protobuf", "google.golang.org/genproto", "/internal"}

func goFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".") && path != ".") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, filepath.ToSlash(path))
		}
		return nil
	})
	te.NoError(t, err)
	return files
}

func isGenerated(path string) bool {
	return strings.HasPrefix(path, "internal/proto/") && strings.HasSuffix(path, ".pb.go")
}

func TestThePublicPackagesAreTheOnesTheSDKPromises(t *testing.T) {
	packages := map[string]bool{}
	for _, path := range goFiles(t) {
		directory := filepath.ToSlash(filepath.Dir(path))
		if strings.HasPrefix(directory, "internal") {
			continue
		}
		packages[directory] = true
	}
	var found []string
	for directory := range packages {
		found = append(found, directory)
	}
	slices.Sort(found)
	te.Equal(t, found, []string{".", "dls", "sre", "webhooks"})
}

func TestNothingGeneratedFromTheProtoOrTheTransportReachesThePublicAPI(t *testing.T) {
	set := token.NewFileSet()
	for _, path := range goFiles(t) {
		if strings.HasPrefix(path, "internal/") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, path, nil, 0)
		te.NoError(t, err)
		imports := map[string]string{}
		for _, spec := range file.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			name := importPath[strings.LastIndex(importPath, "/")+1:]
			if spec.Name != nil {
				name = spec.Name.Name
			}
			imports[name] = importPath
		}
		leaks := func(owner string, expression ast.Expr) {
			ast.Inspect(expression, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				for _, forbidden := range forbiddenImports {
					if strings.Contains(imports[ident.Name], forbidden) {
						t.Errorf("%s: %s exposes %s.%s", path, owner, imports[ident.Name], selector.Sel.Name)
					}
				}
				return true
			})
		}
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				if !typed.Name.IsExported() {
					continue
				}
				if typed.Recv != nil && !receiverExported(typed.Recv) {
					continue
				}
				leaks(typed.Name.Name, typed.Type)
			case *ast.GenDecl:
				for _, spec := range typed.Specs {
					switch item := spec.(type) {
					case *ast.TypeSpec:
						if item.Name.IsExported() {
							leaks(item.Name.Name, exportedPart(item.Type))
						}
					case *ast.ValueSpec:
						for _, name := range item.Names {
							if name.IsExported() && item.Type != nil {
								leaks(name.Name, item.Type)
							}
						}
					}
				}
			}
		}
	}
}

func receiverExported(receiver *ast.FieldList) bool {
	expression := receiver.List[0].Type
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	ident, ok := expression.(*ast.Ident)
	return ok && ident.IsExported()
}

func exportedPart(expression ast.Expr) ast.Expr {
	switch typed := expression.(type) {
	case *ast.StructType:
		fields := &ast.FieldList{}
		for _, field := range typed.Fields.List {
			if len(field.Names) == 0 || slices.ContainsFunc(field.Names, func(name *ast.Ident) bool { return name.IsExported() }) {
				fields.List = append(fields.List, field)
			}
		}
		return &ast.StructType{Fields: fields}
	default:
		return expression
	}
}

func TestTheCodeCarriesNoComments(t *testing.T) {
	directive := regexp.MustCompile(`^//go:(build|generate) `)
	set := token.NewFileSet()
	for _, path := range goFiles(t) {
		if isGenerated(path) {
			continue
		}
		file, err := parser.ParseFile(set, path, nil, parser.ParseComments)
		te.NoError(t, err)
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if !directive.MatchString(comment.Text) {
					t.Errorf("%s: %s", set.Position(comment.Pos()), comment.Text)
				}
			}
		}
	}
}

func TestTheProtoCopiesMatchTheChecksumsInTheirReadme(t *testing.T) {
	readme := string(te.Must(os.ReadFile("proto/README.md")))
	row := regexp.MustCompile("\\| SHA-256 \\| `([0-9a-f]{64})` \\| `([0-9a-f]{64})` \\|").FindStringSubmatch(readme)
	te.True(t, row != nil, "the README has no checksum row")
	for index, name := range []string{"proto/sre_service.proto", "proto/dls_service.proto"} {
		sum := sha256.Sum256(te.Must(os.ReadFile(name)))
		te.Equal(t, hex.EncodeToString(sum[:]), row[index+1])
	}
}

func TestTheGeneratedClientsExposeEveryRPCTheServicesDeclare(t *testing.T) {
	check := func(file string, client reflect.Type) {
		source := string(te.Must(os.ReadFile(file)))
		for _, match := range regexp.MustCompile(`(?m)^\s*rpc (\w+)\(`).FindAllStringSubmatch(source, -1) {
			_, ok := client.MethodByName(match[1])
			te.True(t, ok, "%s declares %s but the client lacks it", file, match[1])
		}
	}
	check("proto/sre_service.proto", reflect.TypeFor[srepb.SharedResourceEngineClient]())
	check("proto/dls_service.proto", reflect.TypeFor[dlspb.DistributedLockingEngineClient]())
}

func TestTheGeneratedCodeLivesInTheInternalPackage(t *testing.T) {
	te.Equal(t, reflect.TypeFor[srepb.TakeRequest]().PkgPath(), "github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb")
	te.Equal(t, reflect.TypeFor[dlspb.AcquireLockRequest]().PkgPath(), "github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb")
}

func TestTheExtensionTravelsInMilliseconds(t *testing.T) {
	_, ok := reflect.TypeFor[srepb.ExtendRequest]().FieldByName("ExtraMs")
	te.True(t, ok, "ExtendRequest.ExtraMs is gone")
}
