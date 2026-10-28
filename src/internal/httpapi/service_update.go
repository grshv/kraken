package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"squid-control.local/squid/internal/policy"
	local "squid-control.local/squid/internal/runtime"
	"squid-control.local/squid/internal/store"
)

// ApplyServiceUpdate is a local CLI operation. Its only policy source is the
// currently confirmed runtime manifest; it never reads or changes the draft.
// Runtime.Apply owns the cross-process lock, expected revision check, complete
// parse/journal/verify and rollback. No session or public API authorizes this path.
func ApplyServiceUpdate(ctx context.Context, st *store.Store, rt *local.Manager, image string) (Operation, error) {
	var op Operation
	manifest, err := rt.ReadManifest()
	if err != nil {
		return op, err
	}
	config, err := policy.Render(manifest.Snapshot, rt.Options)
	if err != nil {
		return op, err
	}
	if config == manifest.Config && manifest.Compiler == policy.CompilerVersion && manifest.ImageDigest == image {
		return op, errors.New("service configuration is already current")
	}
	revision := Revision{ID: store.ID(), Hash: policy.Hash(manifest.Snapshot), Compiler: policy.CompilerVersion, Image: image, Created: stamp(), Snapshot: manifest.Snapshot}
	body, err := json.Marshal(revision)
	if err != nil {
		return op, err
	}
	if err = st.InsertRevision(revision.ID, string(body), "local-cli", "service_update_revision"); err != nil {
		return op, err
	}
	op = Operation{ID: store.ID(), Action: "service_update", Revision: revision.ID, Status: "reloading", Findings: []string{}, Created: stamp(), Updated: stamp()}
	body, err = json.Marshal(op)
	if err != nil {
		return op, err
	}
	if err = st.InsertOperation(op.ID, string(body), "service-update-"+op.ID, policy.Hash(manifest.Snapshot), "local-cli", "service_update"); err != nil {
		return op, err
	}
	op.Status, err = rt.Apply(ctx, op.ID, revision.ID, manifest.Revision, manifest.Snapshot)
	if err != nil {
		op.Findings = []string{"Служебное обновление не завершилось успешно; проверьте журнал применения"}
	}
	op.Updated = stamp()
	body, encodeErr := json.Marshal(op)
	if encodeErr != nil {
		return op, encodeErr
	}
	if saveErr := st.UpdateOperation(op.ID, string(body)); saveErr != nil {
		return op, fmt.Errorf("cannot persist service update outcome; recovery required: %w", saveErr)
	}
	return op, err
}
