package main

import (
	"math/rand"
)

type TileState struct {
	Grid *Grid
}

func (s *TileState) Energy() float64 {
	cost := 0.0
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
	// Keep trying until we find an unconstrained tile
	for {
		idx := rand.Intn(len(s.Grid.Tiles))
		tile := s.Grid.Tiles[idx]

		if !tile.Constrained {
			tile.X = rand.Intn(s.Grid.Width)
			tile.Y = rand.Intn(s.Grid.Height)
			return
		}
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

	return &TileState{Grid: newGrid}
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
