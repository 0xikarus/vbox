# Bash-to-Go migration

The legacy `vmbox.sh`, `RAILWAY.md`, and shell configuration remain as a migration reference. The Go CLI uses JSON contexts rather than sourcing shell code.

1. Build `cmd/vmbox` and put the binary on `PATH` before the legacy script.
2. Translate the non-secret Railway IDs:

   ```bash
   vmbox context add railway --provider railway --project "$VMBOX_PROJECT_ID" --environment "$VMBOX_ENVIRONMENT_ID"
   ```

3. Export `RAILWAY_API_TOKEN` only in the invoking environment or secret manager. Do not copy the old credentials shell file into a repository.
4. Existing services named `vmbox-*` can be inspected by the Railway adapter. Review ownership variables before allowing Go CLI cleanup.
5. Create one box with the desired components, resources, application profiles,
   GitHub identity, and explicit `--instructions PATH` values. The Go CLI saves
   that complete secret-free setup per context and working directory; later
   creation from that same directory can pass `--reuse`. Missing saved local
   files are reported and skipped. Credential
   files and Markdown instruction files remain independent, and Markdown is
   never inferred from an authentication profile.

Back up `/data` before the first destructive operation. The Go CLI requires `clean BOX --yes`, checks ownership metadata, and never implements an unscoped `clean --all`.
