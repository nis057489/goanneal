package main

import (
	"math/rand"
)

type TileState struct {
	Grid    *Grid
	Polygon *Polygon
}

func (s *TileState) Energy() float64 {
	cost := 0.0
	polyTiles := s.Polygon.GetWorldTiles()

	// Check polygon bounds
	for _, t := range polyTiles {
		if t.X < 0 || t.X >= s.Grid.Width || t.Y < 0 || t.Y >= s.Grid.Height {
			return 1000000.0
		}
	}

	// Check overlaps
	for _, t1 := range polyTiles {
		for _, t2 := range s.Grid.Tiles {
			if t1.X == t2.X && t1.Y == t2.Y {
				if t2.Constrained {
					return 1000000.0
				}
				cost += 100.0
			}
		}
	}

	for i, t1 := range s.Grid.Tiles {
		for j, t2 := range s.Grid.Tiles {
			if i == j {
				continue
			}
			if t1.X == t2.X && t1.Y == t2.Y {
				if t1.Constrained || t2.Constrained {
					cost += 1000000.0 // Much higher penalty for constrained tile overlaps
				} else {
					cost += 1000.0
				}
			}
			if t1.Color == t2.Color && adjacent(t1, t2) {
				cost += 1.0
			}
		}
	}
	return cost
}

func (s *TileState) Move() {
	if rand.Float64() < 0.8 {
		// Try moving polygon with smaller steps
		dx := rand.Intn(2)*2 - 1 // -1 or 1
		dy := 0
		if rand.Float64() < 0.5 {
			dx, dy = dy, dx // 50% chance to move vertically instead
		}
		s.Polygon.Move(dx, dy)
	} else {
		s.Polygon.Rotate()
	}
}

func (s *TileState) RepairConstraints() {
	// Find all constraint violations
	violations := make([][]*Tile, 0)
	for i, t1 := range s.Grid.Tiles {
		if !t1.Constrained {
			continue
		}
		for j, t2 := range s.Grid.Tiles {
			if i == j {
				continue
			}
			if t1.X == t2.X && t1.Y == t2.Y {
				violations = append(violations, []*Tile{t1, t2})
			}
		}
	}

	if len(violations) > 0 {
		// Pick a random violation to fix
		violation := violations[rand.Intn(len(violations))]
		// constrained := violation[0]
		moveable := violation[1]

		if moveable.Constrained {
			return // Can't fix if both tiles are constrained
		}

		// Move the unconstrained tile to a random position
		// Keep trying until we find a position that doesn't overlap with any constrained tile
		for tries := 0; tries < 100; tries++ {
			moveable.X = rand.Intn(s.Grid.Width)
			moveable.Y = rand.Intn(s.Grid.Height)

			// Check if new position overlaps with any constrained tile
			hasOverlap := false
			for _, t := range s.Grid.Tiles {
				if t.Constrained && t != moveable && t.X == moveable.X && t.Y == moveable.Y {
					hasOverlap = true
					break
				}
			}

			if !hasOverlap {
				break
			}
		}
	}
}

func (s *TileState) Copy() interface{} {
	newGrid := &Grid{
		Width:  s.Grid.Width,
		Height: s.Grid.Height,
		Tiles:  make([]*Tile, len(s.Grid.Tiles)),
	}

	for i, t := range s.Grid.Tiles {
		newTile := &Tile{
			X:           t.X,
			Y:           t.Y,
			Color:       t.Color,
			Constrained: t.Constrained,
		}
		newGrid.Tiles[i] = newTile
	}

	// Copy polygon
	newPoly := &Polygon{
		Width:    s.Polygon.Width,
		Height:   s.Polygon.Height,
		Rotation: s.Polygon.Rotation,
		PosX:     s.Polygon.PosX,
		PosY:     s.Polygon.PosY,
		Tiles:    make([][]*Tile, len(s.Polygon.Tiles)),
	}

	for y := range s.Polygon.Tiles {
		newPoly.Tiles[y] = make([]*Tile, len(s.Polygon.Tiles[y]))
		for x := range s.Polygon.Tiles[y] {
			t := s.Polygon.Tiles[y][x]
			newPoly.Tiles[y][x] = &Tile{
				X:           t.X,
				Y:           t.Y,
				Color:       t.Color,
				Constrained: t.Constrained,
			}
		}
	}

	return &TileState{
		Grid:    newGrid,
		Polygon: newPoly,
	}
}

// Helper functions
func adjacent(t1, t2 *Tile) bool {
	dx := abs(t1.X - t2.X)
	dy := abs(t1.Y - t2.Y)
	return (dx == 1 && dy == 0) || (dx == 0 && dy == 1)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
