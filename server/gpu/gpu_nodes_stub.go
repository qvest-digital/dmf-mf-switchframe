//go:build (!cgo || !cuda) && !darwin

package gpu

import (
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
)

// NewUploadNode returns nil on non-GPU builds.
func NewUploadNode(ctx *Context, pool *FramePool) switcher.PipelineNode { return nil }

// NewDownloadNode returns nil on non-GPU builds.
func NewDownloadNode(ctx *Context) switcher.PipelineNode { return nil }

// NewSTMapNode returns nil on non-GPU builds.
func NewSTMapNode(ctx *Context, pool *FramePool, registry *stmap.Registry) switcher.PipelineNode {
	return nil
}

// NewKeyNode returns nil on non-GPU builds.
func NewKeyNode() switcher.PipelineNode { return nil }

// NewLayoutNode returns nil on non-GPU builds.
func NewLayoutNode() switcher.PipelineNode { return nil }

// NewCompositorNode returns nil on non-GPU builds.
func NewCompositorNode() switcher.PipelineNode { return nil }
