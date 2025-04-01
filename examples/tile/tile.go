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
