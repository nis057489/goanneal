package main

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
)

// ValidPosition represents a valid polygon placement with its energy score
type ValidPosition struct {
	X, Y     int
	Rotation int
	Energy   float64
}

// Priority Queue implementation
type ValidPositionQueue []*ValidPosition

func (pq ValidPositionQueue) Len() int            { return len(pq) }
func (pq ValidPositionQueue) Less(i, j int) bool  { return pq[i].Energy < pq[j].Energy }
func (pq ValidPositionQueue) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i] }
func (pq *ValidPositionQueue) Push(x interface{}) { *pq = append(*pq, x.(*ValidPosition)) }
func (pq *ValidPositionQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

type TileState struct {
	Grid           *Grid
	Polygon        *Polygon
	Done           bool
	validPositions ValidPositionQueue
	bestPosition   *ValidPosition
}

func (s *TileState) Energy() float64 {
	if s.Done {
		return 0.0
	}

	cost := 0.0
	polyTiles := s.Polygon.GetWorldTiles()

	// Check polygon bounds
	for _, pt := range polyTiles {
		if pt.X < 0 || pt.X >= s.Grid.Width || pt.Y < 0 || pt.Y >= s.Grid.Height {
			return 1000000.0
		}
	}

	// Count tiles that need to be moved
	displacementChain := make(map[*Tile]bool)
	for _, pt := range polyTiles {
		for _, t := range s.Grid.Tiles {
			if t.X == pt.X && t.Y == pt.Y {
				if t.Constrained {
					return 1000000.0 // Invalid position
				}

				// Base cost per displaced tile (1000)
				cost += 1000.0

				// Calculate displacement distance from original position
				if !s.tryDisplaceWithChain(t, displacementChain, 0) {
					// Restore all displaced tiles on failure
					for dt := range displacementChain {
						dt.X = dt.OrigX
						dt.Y = dt.OrigY
						dt.Rotation = 0
						dt.Displaced = false
					}
					return 1000000.0 // Cannot displace
				}

				// Add displacement distance cost
				dx := float64(t.X - t.OrigX)
				dy := float64(t.Y - t.OrigY)
				dist := math.Sqrt(dx*dx + dy*dy)
				cost += dist * 100.0
			}
		}
	}

	// Don't reset positions - they stay displaced unless move is rejected
	return cost
}

func (s *TileState) tryDisplaceWithChain(tile *Tile, chain map[*Tile]bool, rotation float64) bool {
	if tile.Constrained {
		return false
	}

	// Prevent cycles
	if chain[tile] {
		return false
	}
	chain[tile] = true

	// First try rotating in place
	if rotation != 0 {
		canRotate := true
		// Check if rotation would cause collisions
		for _, other := range s.Grid.Tiles {
			if other != tile && other.X == tile.X && other.Y == tile.Y {
				canRotate = false
				break
			}
		}
		if canRotate {
			tile.Rotation = rotation
			tile.Displaced = true
			return true
		}
	}

	// Calculate direction away from polygon center
	polygonCenterX := float64(s.Polygon.PosX) + float64(s.Polygon.Width)/2
	polygonCenterY := float64(s.Polygon.PosY) + float64(s.Polygon.Height)/2
	tileX := float64(tile.X)
	tileY := float64(tile.Y)

	// Get displacement direction vector
	dx := tileX - polygonCenterX
	dy := tileY - polygonCenterY

	// Only try adjacent positions (no diagonals)
	moves := [][2]int{{0, 1}, {1, 0}, {0, -1}, {-1, 0}}

	// Sort moves by alignment with displacement vector
	moveScores := make([]float64, len(moves))
	for i, move := range moves {
		dotProduct := float64(move[0])*dx + float64(move[1])*dy
		moveScores[i] = dotProduct
	}

	// Try moves in order of best alignment with desired direction
	for i := 0; i < len(moves); i++ {
		bestScore := moveScores[0]
		bestIdx := 0
		for j := 1; j < len(moveScores); j++ {
			if moveScores[j] > bestScore {
				bestScore = moveScores[j]
				bestIdx = j
			}
		}

		move := moves[bestIdx]
		moveScores[bestIdx] = -math.MaxFloat64 // Mark as used

		newX := tile.X + move[0]
		newY := tile.Y + move[1]

		if newX < 0 || newX >= s.Grid.Width || newY < 0 || newY >= s.Grid.Height {
			continue
		}

		// Check if new position has a tile that needs to be displaced
		blocked := false
		for _, other := range s.Grid.Tiles {
			if other.X == newX && other.Y == newY {
				blocked = true
				// Try to recursively displace the blocking tile
				if s.tryDisplaceWithChain(other, chain, rotation*0.8) {
					blocked = false
				}
				break
			}
		}

		if !blocked {
			tile.X = newX
			tile.Y = newY
			tile.Rotation = rotation
			tile.Displaced = true
			return true
		}
	}

	return false
}

func (s *TileState) isValidPosition() bool {
	for _, pt := range s.Polygon.GetWorldTiles() {
		// Check bounds
		if pt.X < 0 || pt.X >= s.Grid.Width || pt.Y < 0 || pt.Y >= s.Grid.Height {
			return false
		}
		// Check constrained tile overlaps
		for _, t := range s.Grid.Tiles {
			if t.Constrained && t.X == pt.X && t.Y == pt.Y {
				return false
			}
		}
	}
	return true
}

func (s *TileState) Move() {
	if s.Done {
		if s.bestPosition == nil && len(s.validPositions) > 0 {
			// Find best position when simulation ends
			tmp := make(ValidPositionQueue, len(s.validPositions))
			copy(tmp, s.validPositions)
			heap.Init(&tmp)
			s.bestPosition = heap.Pop(&tmp).(*ValidPosition)

			// Move to best position
			s.Polygon.PosX = s.bestPosition.X
			s.Polygon.PosY = s.bestPosition.Y
			s.Polygon.Rotation = s.bestPosition.Rotation
			s.Polygon.isDirty = true
		}
		return
	}

	if !s.Polygon.HasValidMovesLeft(s.Grid) {
		s.Done = true
		return
	}

	if rand.Float64() < 0.7 {
		// Try to move a movable tile away from the polygon
		for tries := 0; tries < 10; tries++ {
			idx := rand.Intn(len(s.Grid.Tiles))
			tile := s.Grid.Tiles[idx]
			if !tile.Constrained {
				oldX, oldY := tile.X, tile.Y
				dx := rand.Intn(3) - 1
				dy := rand.Intn(3) - 1
				newX := tile.X + dx
				newY := tile.Y + dy

				// Check if new position is valid and not on a constrained tile
				if newX >= 0 && newX < s.Grid.Width && newY >= 0 && newY < s.Grid.Height {
					canMove := true
					for _, t := range s.Grid.Tiles {
						if t.Constrained && t.X == newX && t.Y == newY {
							canMove = false
							break
						}
					}
					if canMove {
						tile.X = newX
						tile.Y = newY
						break
					}
				}
				tile.X, tile.Y = oldX, oldY
			}
		}
	} else {
		oldX, oldY, oldRot := s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation
		moved := false

		for tries := 0; tries < 10; tries++ {
			if rand.Float64() < 0.5 {
				// Attempt small movement
				dx := rand.Intn(2)*2 - 1 // -1 or 1
				dy := 0
				if rand.Float64() < 0.5 {
					dx, dy = dy, dx
				}

				// Check if move would be in bounds
				w, h := s.Polygon.Width, s.Polygon.Height
				if s.Polygon.Rotation == 90 || s.Polygon.Rotation == 270 {
					w, h = h, w
				}
				newX := s.Polygon.PosX + dx
				newY := s.Polygon.PosY + dy

				if newX >= 0 && newX+w <= s.Grid.Width && newY >= 0 && newY+h <= s.Grid.Height {
					s.Polygon.Move(dx, dy)
				}
			} else {
				s.Polygon.Rotate()
			}

			configKey := s.Polygon.GetConfigKey()
			if !s.Polygon.triedConfigs[configKey] && s.isValidPosition() {
				s.Polygon.triedConfigs[configKey] = true
				moved = true
				// Store valid position and its energy
				energy := s.Energy()
				validPos := &ValidPosition{
					X:        s.Polygon.PosX,
					Y:        s.Polygon.PosY,
					Rotation: s.Polygon.Rotation,
					Energy:   energy,
				}
				heap.Push(&s.validPositions, validPos)
				// Update heap after pushing
				heap.Init(&s.validPositions)
				break
			}
		}

		if !moved {
			s.Polygon.PosX = oldX
			s.Polygon.PosY = oldY
			s.Polygon.Rotation = oldRot
			s.Polygon.isDirty = true
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
			OrigX:       t.OrigX,
			OrigY:       t.OrigY,
			Color:       t.Color,
			Constrained: t.Constrained,
			Rotation:    t.Rotation,
			Displaced:   t.Displaced,
		}
		newGrid.Tiles[i] = newTile
	}

	// Copy polygon
	newPoly := &Polygon{
		Width:        s.Polygon.Width,
		Height:       s.Polygon.Height,
		Rotation:     s.Polygon.Rotation,
		PosX:         s.Polygon.PosX,
		PosY:         s.Polygon.PosY,
		isDirty:      s.Polygon.isDirty,
		triedConfigs: make(map[string]bool),
		Tiles:        make([][]*Tile, len(s.Polygon.Tiles)),
	}

	// Copy tried configurations
	for k, v := range s.Polygon.triedConfigs {
		newPoly.triedConfigs[k] = v
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

	// Deep copy of validPositions queue
	newValidPositions := make(ValidPositionQueue, len(s.validPositions))
	copy(newValidPositions, s.validPositions)
	heap.Init(&newValidPositions)

	return &TileState{
		Grid:           newGrid,
		Polygon:        newPoly,
		Done:           s.Done,
		validPositions: newValidPositions,
		bestPosition:   s.bestPosition,
	}
}

func (s *TileState) PrintBestPositions(n int) {
	if len(s.validPositions) == 0 {
		fmt.Printf("\nNo valid positions found!\n")
		return
	}

	fmt.Printf("\nSimulation complete! Found %d valid positions.\n", len(s.validPositions))
	fmt.Printf("Top %d best positions:\n", n)

	// Create a temp copy to preserve the original queue
	tmp := make(ValidPositionQueue, len(s.validPositions))
	copy(tmp, s.validPositions)
	heap.Init(&tmp)

	for i := 0; i < n && tmp.Len() > 0; i++ {
		pos := heap.Pop(&tmp).(*ValidPosition)
		fmt.Printf("%d. Position: (%d,%d) Rotation: %d° Energy: %.2f (tiles to move: %.0f)\n",
			i+1, pos.X, pos.Y, pos.Rotation, pos.Energy, pos.Energy/1000.0)
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
