package main

const (
	maxNodesPerCell = 4
	minNodesPerCell = 2
)

// SpatialNode represents a node in the B* tree spatial index
type SpatialNode struct {
	bounds   Rect
	tiles    []*Tile
	children []*SpatialNode
	isLeaf   bool
	parent   *SpatialNode
}

type Rect struct {
	x, y, width, height int
}

// Returns true if this rectangle contains the given point
func (r *Rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.width &&
		y >= r.y && y < r.y+r.height
}

// Returns true if this rectangle overlaps another rectangle
func (r *Rect) overlaps(other *Rect) bool {
	return !(other.x >= r.x+r.width ||
		other.x+other.width <= r.x ||
		other.y >= r.y+r.height ||
		other.y+other.height <= r.y)
}

// SpatialIndex manages the B* tree spatial partitioning
type SpatialIndex struct {
	root *SpatialNode
}

func NewSpatialIndex(width, height int) *SpatialIndex {
	if width <= 0 || height <= 0 {
		return nil
	}

	si := &SpatialIndex{}
	si.root = &SpatialNode{
		bounds: Rect{x: 0, y: 0, width: width, height: height},
		tiles:  make([]*Tile, 0),
		isLeaf: true,
	}

	return si
}

func (si *SpatialIndex) Insert(tile *Tile) {
	if si == nil || si.root == nil {
		panic("Cannot insert into uninitialized spatial index")
	}

	node := si.findLeafNode(si.root, tile.X, tile.Y)
	node.tiles = append(node.tiles, tile)

	if len(node.tiles) > maxNodesPerCell {
		si.split(node)
	}
}

func (si *SpatialIndex) Remove(tile *Tile) {
	node := si.findLeafNode(si.root, tile.X, tile.Y)
	for i, t := range node.tiles {
		if t == tile {
			node.tiles = append(node.tiles[:i], node.tiles[i+1:]...)
			break
		}
	}

	if node.parent != nil && len(node.tiles) < minNodesPerCell {
		si.merge(node)
	}
}

func (si *SpatialIndex) Update(tile *Tile, oldX, oldY int) {
	si.Remove(tile)
	si.Insert(tile)
}

func (si *SpatialIndex) QueryPoint(x, y int) []*Tile {
	return si.findLeafNode(si.root, x, y).tiles
}

func (si *SpatialIndex) QueryRect(rect *Rect) []*Tile {
	results := make([]*Tile, 0)
	si.queryRectNode(si.root, rect, &results)
	return results
}

func (si *SpatialIndex) queryRectNode(node *SpatialNode, rect *Rect, results *[]*Tile) {
	if !node.bounds.overlaps(rect) {
		return
	}

	if node.isLeaf {
		for _, tile := range node.tiles {
			if rect.contains(tile.X, tile.Y) {
				*results = append(*results, tile)
			}
		}
	} else {
		for _, child := range node.children {
			si.queryRectNode(child, rect, results)
		}
	}
}

func (si *SpatialIndex) findLeafNode(node *SpatialNode, x, y int) *SpatialNode {
	if node.isLeaf {
		return node
	}

	for _, child := range node.children {
		if child.bounds.contains(x, y) {
			return si.findLeafNode(child, x, y)
		}
	}
	return node
}

func (si *SpatialIndex) split(node *SpatialNode) {
	// Split into 4 quadrants
	w := node.bounds.width / 2
	h := node.bounds.height / 2

	node.children = []*SpatialNode{
		{
			bounds: Rect{node.bounds.x, node.bounds.y, w, h},
			tiles:  make([]*Tile, 0),
			isLeaf: true,
			parent: node,
		},
		{
			bounds: Rect{node.bounds.x + w, node.bounds.y, w, h},
			tiles:  make([]*Tile, 0),
			isLeaf: true,
			parent: node,
		},
		{
			bounds: Rect{node.bounds.x, node.bounds.y + h, w, h},
			tiles:  make([]*Tile, 0),
			isLeaf: true,
			parent: node,
		},
		{
			bounds: Rect{node.bounds.x + w, node.bounds.y + h, w, h},
			tiles:  make([]*Tile, 0),
			isLeaf: true,
			parent: node,
		},
	}

	// Redistribute tiles to children
	for _, tile := range node.tiles {
		for _, child := range node.children {
			if child.bounds.contains(tile.X, tile.Y) {
				child.tiles = append(child.tiles, tile)
				break
			}
		}
	}

	node.tiles = nil
	node.isLeaf = false
}

func (si *SpatialIndex) merge(node *SpatialNode) {
	parent := node.parent
	if parent == nil {
		return
	}

	// Collect all tiles from siblings
	allTiles := make([]*Tile, 0)
	for _, child := range parent.children {
		allTiles = append(allTiles, child.tiles...)
	}

	// Convert parent back to leaf
	parent.tiles = allTiles
	parent.children = nil
	parent.isLeaf = true
}
