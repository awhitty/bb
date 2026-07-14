# Contributing

`bb` is a small personal tool shared in public. Focused bug reports and pull requests are welcome, but there is no promise that a proposed feature will fit the project.

Before opening a change:

1. Search existing issues.
2. Keep the change focused on one observable behavior.
3. Add or update tests for behavior changes.
4. Run the local gates below.

```sh
go mod tidy
go mod verify
go vet ./...
go test ./...
go test -race ./...
go build ./...
```

Please do not include real conversation transcripts, local credentials, private Beads data, or absolute paths from your machine in fixtures or bug reports.

By contributing, you agree that your contribution is licensed under the MIT License in this repository.
