package main

type Color int

const (
	Red Color = iota
	Blue
	Green
	Yellow
)

type Tile struct {
	X, Y        int
	Color       Color
	Constrained bool
}

type Polygon struct {
	Tiles       [][]*Tile // 2D grid of tiles representing the polygon
	Width       int
	Height      int
	Rotation    int // 0, 90, 180, 270 degrees
	PosX, PosY  int // Position of top-left corner
	cachedTiles []*Tile
	isDirty     bool
}

func NewRectangle(width, height int, color Color) *Polygon {
	tiles := make([][]*Tile, height)
	for y := range tiles {
		tiles[y] = make([]*Tile, width)
		for x := range tiles[y] {
			tiles[y][x] = &Tile{
				X:           x,
				Y:           y,
				Color:       color,
				Constrained: false,
			}
		}
	}
	return &Polygon{
		Tiles:   tiles,
		Width:   width,
		Height:  height,
		isDirty: true,
	}
}

func (p *Polygon) Rotate() {
	p.Rotation = (p.Rotation + 90) % 360
	p.isDirty = true
}

func (p *Polygon) Move(dx, dy int) {
	newX := p.PosX + dx
	newY := p.PosY + dy

	// Check if any part of the polygon would go off screen
	w, h := p.Width, p.Height
	if p.Rotation == 90 || p.Rotation == 270 {
		w, h = h, w
	}

	// Enforce bounds
	if newX >= 0 && (newX+w) <= 20 && newY >= 0 && (newY+h) <= 20 {
		p.PosX = newX
		p.PosY = newY
		p.isDirty = true
	}
}

func (p *Polygon) GetWorldTiles() []*Tile {
	if !p.isDirty && p.cachedTiles != nil {
		return p.cachedTiles
	}
	tiles := make([]*Tile, 0)
	for y := 0; y < p.Height; y++ {
		for x := 0; x < p.Width; x++ {
			worldX, worldY := p.transformToWorld(x, y)
			if worldX >= 0 && worldX < 20 && worldY >= 0 && worldY < 20 {
				origX, origY := x, y
				// Handle rotated coordinate lookup
				switch p.Rotation {
				case 90:
					origX, origY = y, p.Width-1-x
				case 180:
					origX, origY = p.Width-1-x, p.Height-1-y
				case 270:
					origX, origY = p.Height-1-y, x
				}
				if origY < len(p.Tiles) && origX < len(p.Tiles[origY]) {
					tile := &Tile{
						X:           worldX,
						Y:           worldY,
						Color:       p.Tiles[origY][origX].Color,
						Constrained: false,
					}
					tiles = append(tiles, tile)
				}
			}
		}
	}
	p.cachedTiles = tiles
	p.isDirty = false
	return tiles
}

func (p *Polygon) transformToWorld(x, y int) (int, int) {
	switch p.Rotation {
	case 0:
		return x + p.PosX, y + p.PosY
	case 90:
		return p.PosX + y, p.PosY + (p.Width - 1 - x)
	case 180:
		return p.PosX + (p.Width - 1 - x), p.PosY + (p.Height - 1 - y)
	case 270:
		return p.PosX + (p.Height - 1 - y), p.PosY + x
	}
	return x + p.PosX, y + p.PosY
}

type Grid struct {
	Width  int
	Height int
	Tiles  []*Tile
}

func NewGrid(width, height int) *Grid {
	return &Grid{
		Width:  width,
		Height: height,
		Tiles:  make([]*Tile, 0),
	}
}

func (g *Grid) AddTile(x, y int, color Color, constrained bool) {
	g.Tiles = append(g.Tiles, &Tile{
		X:           x,
		Y:           y,
		Color:       color,
		Constrained: constrained,
	})
}

func (g *Grid) GetTileAt(x, y int) *Tile {
	for _, t := range g.Tiles {
		if t.X == x && t.Y == y {
			return t
		}
	}
	return nil
}
