package factory

import (
	"context"
	"io"
	"os"

	"github.com/0xikarus/vmbox-service/internal/factory/assets"
)

// PrivateAssets adapts the filesystem implementation without coupling it to HTTP.
type PrivateAssets struct{ Store *assets.Store }

func (a PrivateAssets) Put(ctx context.Context, account, name string, r io.Reader) (AssetRef, error) {
	v, err := a.Store.Put(ctx, account, name, r)
	return AssetRef(v), err
}
func (a PrivateAssets) Open(ctx context.Context, account, id string) (*os.File, AssetRef, error) {
	f, v, err := a.Store.Open(ctx, account, id)
	return f, AssetRef(v), err
}
