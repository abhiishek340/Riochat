# Area template

The working template is the reference area at `internal/features/example`.
Copy that directory to `internal/features/<area>`, rename the package and the
table, and replace the handlers. It is never registered in `internal/registry/registry.go`.

Files an area owns (and may freely create, edit and delete):

- `mount.go`        - `func Mount(d deps.Deps) error`
- `migrations.go`   - `func Migrations() []migrate.Step`
- `models.go`, `service.go`, `handlers.go`, ... - anything else
- `*_test.go`       - the area's own tests (use `internal/testkit`)

An area must not edit files outside its directory. Anything it needs from the
platform is on `deps.Deps`. Anything it needs registered globally (an error
code) it declares itself with `apperr.Define` in its own package.
