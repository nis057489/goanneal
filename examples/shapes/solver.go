package main

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
)

const hardInvalidEnergy = 1e12

// ValidPosition represents a valid polygon placement with its energy score
type ValidPosition struct {
	X, Y     int
	Rotation int
	Energy   float64
}

type polygonPoseCandidate struct {
	x, y, rot int
	oTiles    int
	oObjects  int
	energy    float64
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
	energyMemo     map[string]float64
}

func (s *TileState) Energy() float64 {
	if s.Done {
		return 0.0
	}

	stateKey := s.MemoKey()
	if cached, ok := s.getMemoizedEnergy(stateKey); ok {
		return cached
	}

	cost := 0.0
	polyTiles := s.Polygon.GetWorldTiles()

	// Check polygon bounds and constrained overlaps.
	for _, pt := range polyTiles {
		if pt.X < 0 || pt.X >= s.Grid.Width || pt.Y < 0 || pt.Y >= s.Grid.Height {
			s.memoizeEnergy(stateKey, hardInvalidEnergy)
			return hardInvalidEnergy
		}
	}

	overlapTiles := 0
	overlapObjects := make(map[int]bool)
	for _, pt := range polyTiles {
		for _, t := range s.Grid.Tiles {
			if t.X != pt.X || t.Y != pt.Y {
				continue
			}
			if t.Constrained {
				s.memoizeEnergy(stateKey, hardInvalidEnergy)
				return hardInvalidEnergy
			}
			overlapTiles++
			overlapObjects[t.ObjectID] = true
		}
	}

	// Primary objective: clear incoming footprint completely.
	if overlapTiles > 0 {
		cost += float64(overlapTiles) * 50000.0
		cost += float64(len(overlapObjects)) * 12000.0
	}

	// Secondary objective: minimal disturbance of original layout.
	movedObjects := make(map[int]bool)
	totalDist := 0.0
	for _, t := range s.Grid.Tiles {
		if t.Constrained {
			continue
		}
		dx := float64(t.X - t.OrigX)
		dy := float64(t.Y - t.OrigY)
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist > 0 {
			movedObjects[t.ObjectID] = true
		}
		totalDist += dist
	}

	distWeight := 45.0
	objectWeight := 400.0
	if overlapTiles > 0 {
		distWeight = 10.0
		objectWeight = 90.0
	}

	cost += totalDist * distWeight
	cost += float64(len(movedObjects)) * objectWeight

	s.memoizeEnergy(stateKey, cost)
	return cost
}

func (s *TileState) getMemoizedEnergy(key string) (float64, bool) {
	if s.energyMemo == nil {
		return 0, false
	}
	v, ok := s.energyMemo[key]
	return v, ok
}

func (s *TileState) memoizeEnergy(key string, value float64) {
	if s.energyMemo == nil {
		s.energyMemo = make(map[string]float64)
	}
	s.energyMemo[key] = value
}

func (s *TileState) MemoKey() string {
	tiles := make([]*Tile, len(s.Grid.Tiles))
	copy(tiles, s.Grid.Tiles)
	sort.Slice(tiles, func(i, j int) bool {
		if tiles[i].ObjectID != tiles[j].ObjectID {
			return tiles[i].ObjectID < tiles[j].ObjectID
		}
		if tiles[i].X != tiles[j].X {
			return tiles[i].X < tiles[j].X
		}
		return tiles[i].Y < tiles[j].Y
	})

	var b strings.Builder
	b.Grow(len(tiles) * 12)
	fmt.Fprintf(&b, "p:%d,%d,%d|", s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation)
	for _, t := range tiles {
		fmt.Fprintf(&b, "%d:%d,%d;", t.ObjectID, t.X, t.Y)
	}
	return b.String()
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

	overlapTiles, overlapObjects := s.overlapStats()

	if overlapTiles > 0 {
		if rand.Float64() < 0.25 {
			if s.repositionPolygonForClearance(14) {
				return
			}
		}
		if s.clearBlockersIteratively(10) {
			return
		}
		if s.aggressiveClearUnderPolygon(8) {
			return
		}
		if s.pushBlockersFromPolygon(overlapObjects, 18) {
			return
		}
		if s.pushNearbyObjectsFromPolygon(6, 10) {
			return
		}
		if s.randomMacroObjectMove(20) {
			return
		}
		if s.forcePerturb() {
			return
		}
		if s.repositionPolygonForClearance(24) {
			return
		}
		return
	}

	if rand.Float64() < 0.92 {
		if s.pushBlockersFromPolygon(4, 10) {
			return
		}

		// Try to move a movable object one step
		for tries := 0; tries < 20; tries++ {
			idx := rand.Intn(len(s.Grid.Tiles))
			tile := s.Grid.Tiles[idx]
			if !tile.Constrained {
				moves := [][2]int{{0, 1}, {1, 0}, {0, -1}, {-1, 0}}
				move := moves[rand.Intn(len(moves))]
				if s.moveObjectIfPossible(tile.ObjectID, move[0], move[1]) {
					break
				}
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
			if s.isValidPosition() {
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

func (s *TileState) repositionPolygonForClearance(randomTries int) bool {
	curX, curY, curRot := s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation
	curOverlapTiles, curOverlapObjs := s.overlapStats()
	curEnergy := s.Energy()

	best := polygonPoseCandidate{x: curX, y: curY, rot: curRot, oTiles: curOverlapTiles, oObjects: curOverlapObjs, energy: curEnergy}
	foundBetter := false

	localMoves := [][3]int{{1, 0, curRot}, {-1, 0, curRot}, {0, 1, curRot}, {0, -1, curRot}, {0, 0, (curRot + 90) % 360}}
	for _, m := range localMoves {
		nx := curX + m[0]
		ny := curY + m[1]
		nrot := m[2]
		if !s.tryPolygonCandidate(nx, ny, nrot, &best) {
			continue
		}
		foundBetter = true
	}

	for i := 0; i < randomTries; i++ {
		nrot := (rand.Intn(4) * 90) % 360
		w, h := s.polygonDimsAtRotation(nrot)
		nx := rand.Intn(maxInt(1, s.Grid.Width-w+1))
		ny := rand.Intn(maxInt(1, s.Grid.Height-h+1))
		if !s.tryPolygonCandidate(nx, ny, nrot, &best) {
			continue
		}
		foundBetter = true
	}

	if !foundBetter {
		s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation = curX, curY, curRot
		s.Polygon.isDirty = true
		return false
	}

	improved := best.oTiles < curOverlapTiles ||
		(best.oTiles == curOverlapTiles && best.oObjects < curOverlapObjs) ||
		(best.oTiles == curOverlapTiles && best.oObjects == curOverlapObjs && best.energy < curEnergy)

	if !improved && rand.Float64() > 0.15 {
		s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation = curX, curY, curRot
		s.Polygon.isDirty = true
		return false
	}

	s.Polygon.PosX = best.x
	s.Polygon.PosY = best.y
	s.Polygon.Rotation = best.rot
	s.Polygon.isDirty = true
	return true
}

func (s *TileState) tryPolygonCandidate(nx, ny, nrot int, best *polygonPoseCandidate) bool {
	oldX, oldY, oldRot := s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation
	s.Polygon.PosX = nx
	s.Polygon.PosY = ny
	s.Polygon.Rotation = nrot
	s.Polygon.isDirty = true

	if !s.isValidPosition() {
		s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation = oldX, oldY, oldRot
		s.Polygon.isDirty = true
		return false
	}

	oTiles, oObjs := s.overlapStats()
	e := s.Energy()

	better := oTiles < best.oTiles ||
		(oTiles == best.oTiles && oObjs < best.oObjects) ||
		(oTiles == best.oTiles && oObjs == best.oObjects && e < best.energy)

	if better {
		best.x, best.y, best.rot = nx, ny, nrot
		best.oTiles, best.oObjects, best.energy = oTiles, oObjs, e
	}

	s.Polygon.PosX, s.Polygon.PosY, s.Polygon.Rotation = oldX, oldY, oldRot
	s.Polygon.isDirty = true
	return true
}

func (s *TileState) polygonDimsAtRotation(rot int) (int, int) {
	w, h := s.Polygon.Width, s.Polygon.Height
	if rot == 90 || rot == 270 {
		w, h = h, w
	}
	return w, h
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *TileState) clearBlockersIteratively(rounds int) bool {
	changed := false

	for r := 0; r < rounds; r++ {
		before := s.OverlapCount()
		if before == 0 {
			return changed
		}

		overlapCounts := s.overlappingObjectCounts()
		type blockedObj struct {
			id    int
			count int
		}
		blockerList := make([]blockedObj, 0, len(overlapCounts))
		for id, count := range overlapCounts {
			blockerList = append(blockerList, blockedObj{id: id, count: count})
		}
		sort.Slice(blockerList, func(i, j int) bool {
			if blockerList[i].count == blockerList[j].count {
				return blockerList[i].id < blockerList[j].id
			}
			return blockerList[i].count > blockerList[j].count
		})

		roundProgress := false
		for _, b := range blockerList {
			if s.relocateBlockingObject(b.id, 30) {
				roundProgress = true
				changed = true
				if s.OverlapCount() < before {
					break
				}
			}
		}

		if s.OverlapCount() == 0 {
			return true
		}

		if roundProgress {
			continue
		}

		if s.pushNearbyObjectsFromPolygon(10, 14) || s.randomMacroObjectMove(24) {
			changed = true
			continue
		}

		break
	}

	return changed
}

func (s *TileState) relocateBlockingObject(objectID, maxSteps int) bool {
	dirs := s.preferredExitDirections(objectID)
	type plan struct {
		dx, dy       int
		steps        int
		overlapAfter int
	}

	best := plan{steps: 0, overlapAfter: math.MaxInt32}
	baseOverlap := s.OverlapCount()

	for _, d := range dirs {
		sim := s.Copy().(*TileState)
		moved := 0
		for moved < maxSteps {
			if !sim.moveObjectIfPossible(objectID, d[0], d[1]) {
				break
			}
			moved++
			if sim.OverlapCount() < baseOverlap {
				break
			}
		}

		if moved == 0 {
			continue
		}

		after := sim.OverlapCount()
		if after < best.overlapAfter ||
			(after == best.overlapAfter && moved < best.steps) {
			best = plan{dx: d[0], dy: d[1], steps: moved, overlapAfter: after}
		}
	}

	if best.steps == 0 {
		return false
	}

	applied := 0
	for i := 0; i < best.steps; i++ {
		if !s.moveObjectIfPossible(objectID, best.dx, best.dy) {
			break
		}
		applied++
		if s.OverlapCount() < baseOverlap {
			break
		}
	}

	return applied > 0
}

func (s *TileState) preferredExitDirections(objectID int) [][2]int {
	objectTiles := s.getObjectTiles(objectID)
	if len(objectTiles) == 0 {
		return [][2]int{{0, 1}, {1, 0}, {0, -1}, {-1, 0}}
	}

	var sx, sy float64
	for _, t := range objectTiles {
		sx += float64(t.X)
		sy += float64(t.Y)
	}
	objX := sx / float64(len(objectTiles))
	objY := sy / float64(len(objectTiles))

	left := s.Polygon.PosX
	right := s.Polygon.PosX + s.Polygon.Width - 1
	top := s.Polygon.PosY
	bottom := s.Polygon.PosY + s.Polygon.Height - 1

	type dir struct {
		dxy  [2]int
		dist float64
	}
	dirs := []dir{
		{dxy: [2]int{-1, 0}, dist: math.Abs(objX - float64(left))},
		{dxy: [2]int{1, 0}, dist: math.Abs(float64(right) - objX)},
		{dxy: [2]int{0, -1}, dist: math.Abs(objY - float64(top))},
		{dxy: [2]int{0, 1}, dist: math.Abs(float64(bottom) - objY)},
	}

	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].dist < dirs[j].dist
	})

	ordered := make([][2]int, 0, 4)
	for _, d := range dirs {
		ordered = append(ordered, d.dxy)
	}
	return ordered
}

func (s *TileState) forcePerturb() bool {
	if s.randomMacroObjectMove(32) {
		return true
	}
	if s.pushNearbyObjectsFromPolygon(10, 16) {
		return true
	}

	for tries := 0; tries < 60; tries++ {
		idx := rand.Intn(len(s.Grid.Tiles))
		tile := s.Grid.Tiles[idx]
		if tile.Constrained {
			continue
		}
		if s.shakeObject(tile.ObjectID, 18) {
			return true
		}
	}

	return false
}

func (s *TileState) pushBlockersFromPolygon(maxObjects, maxSteps int) bool {
	overlap := s.overlappingObjectIDs()
	if len(overlap) == 0 {
		return false
	}

	ids := make([]int, 0, len(overlap))
	for id := range overlap {
		ids = append(ids, id)
	}
	rand.Shuffle(len(ids), func(i, j int) {
		ids[i], ids[j] = ids[j], ids[i]
	})

	if maxObjects > len(ids) {
		maxObjects = len(ids)
	}

	anyMoved := false
	for i := 0; i < maxObjects; i++ {
		if s.pushObjectAwayFromPolygon(ids[i], maxSteps) {
			anyMoved = true
		}
	}

	return anyMoved
}

func (s *TileState) overlappingObjectIDs() map[int]bool {
	overlap := make(map[int]bool)
	for _, pt := range s.Polygon.GetWorldTiles() {
		for _, t := range s.Grid.Tiles {
			if t.Constrained {
				continue
			}
			if t.X == pt.X && t.Y == pt.Y {
				overlap[t.ObjectID] = true
			}
		}
	}
	return overlap
}

func (s *TileState) overlapStats() (int, int) {
	overlapTiles := 0
	overlapObjects := make(map[int]bool)
	for _, pt := range s.Polygon.GetWorldTiles() {
		for _, t := range s.Grid.Tiles {
			if t.Constrained {
				continue
			}
			if t.X == pt.X && t.Y == pt.Y {
				overlapTiles++
				overlapObjects[t.ObjectID] = true
			}
		}
	}
	return overlapTiles, len(overlapObjects)
}

func (s *TileState) OverlapCount() int {
	count, _ := s.overlapStats()
	return count
}

func (s *TileState) pushObjectAwayFromPolygon(objectID, maxSteps int) bool {
	objectTiles := s.getObjectTiles(objectID)
	if len(objectTiles) == 0 {
		return false
	}

	var sumX, sumY float64
	for _, t := range objectTiles {
		sumX += float64(t.X)
		sumY += float64(t.Y)
	}
	objX := sumX / float64(len(objectTiles))
	objY := sumY / float64(len(objectTiles))

	polyX := float64(s.Polygon.PosX) + float64(s.Polygon.Width)/2
	polyY := float64(s.Polygon.PosY) + float64(s.Polygon.Height)/2

	left := s.Polygon.PosX
	right := s.Polygon.PosX + s.Polygon.Width - 1
	top := s.Polygon.PosY
	bottom := s.Polygon.PosY + s.Polygon.Height - 1

	insideX := int(math.Round(objX)) >= left && int(math.Round(objX)) <= right
	insideY := int(math.Round(objY)) >= top && int(math.Round(objY)) <= bottom

	type dir struct {
		dx, dy int
		score  float64
	}
	dirs := make([]dir, 0, 4)
	if insideX && insideY {
		dLeft := objX - float64(left)
		dRight := float64(right) - objX
		dUp := objY - float64(top)
		dDown := float64(bottom) - objY
		dirs = append(dirs,
			dir{dx: -1, dy: 0, score: -dLeft},
			dir{dx: 1, dy: 0, score: -dRight},
			dir{dx: 0, dy: -1, score: -dUp},
			dir{dx: 0, dy: 1, score: -dDown},
		)
	} else {
		vx := objX - polyX
		vy := objY - polyY
		dirs = append(dirs,
			dir{dx: 0, dy: 1, score: vy},
			dir{dx: 1, dy: 0, score: vx},
			dir{dx: 0, dy: -1, score: -vy},
			dir{dx: -1, dy: 0, score: -vx},
		)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].score > dirs[j].score
	})

	moved := false
	for _, d := range dirs {
		steps := 0
		for steps < maxSteps {
			if !s.moveObjectIfPossible(objectID, d.dx, d.dy) {
				break
			}
			moved = true
			steps++
		}
		if moved {
			return true
		}
	}

	return false
}

func (s *TileState) pushNearbyObjectsFromPolygon(maxObjects, maxSteps int) bool {
	polyCenterX := float64(s.Polygon.PosX) + float64(s.Polygon.Width)/2
	polyCenterY := float64(s.Polygon.PosY) + float64(s.Polygon.Height)/2

	type candidate struct {
		id    int
		score float64
	}

	byObject := make(map[int][]*Tile)
	for _, t := range s.Grid.Tiles {
		if t.Constrained {
			continue
		}
		byObject[t.ObjectID] = append(byObject[t.ObjectID], t)
	}

	candidates := make([]candidate, 0, len(byObject))
	for id, tiles := range byObject {
		var sx, sy float64
		for _, t := range tiles {
			sx += float64(t.X)
			sy += float64(t.Y)
		}
		cx := sx / float64(len(tiles))
		cy := sy / float64(len(tiles))
		dx := cx - polyCenterX
		dy := cy - polyCenterY
		dist := math.Sqrt(dx*dx + dy*dy)
		candidates = append(candidates, candidate{id: id, score: dist})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score < candidates[j].score
	})

	if len(candidates) > maxObjects {
		candidates = candidates[:maxObjects]
	}

	anyMoved := false
	for _, c := range candidates {
		if s.pushObjectAwayFromPolygon(c.id, maxSteps) {
			anyMoved = true
		}
	}
	return anyMoved
}

func (s *TileState) aggressiveClearUnderPolygon(rounds int) bool {
	changed := false

	for r := 0; r < rounds; r++ {
		overlapCounts := s.overlappingObjectCounts()
		if len(overlapCounts) == 0 {
			return changed
		}

		type blocked struct {
			id    int
			count int
		}
		blockedObjects := make([]blocked, 0, len(overlapCounts))
		for id, count := range overlapCounts {
			blockedObjects = append(blockedObjects, blocked{id: id, count: count})
		}
		sort.Slice(blockedObjects, func(i, j int) bool {
			return blockedObjects[i].count > blockedObjects[j].count
		})

		limit := 3
		if limit > len(blockedObjects) {
			limit = len(blockedObjects)
		}

		roundMoved := false
		for i := 0; i < limit; i++ {
			id := blockedObjects[i].id
			if s.pushObjectAwayFromPolygon(id, 26) {
				roundMoved = true
				changed = true
				continue
			}
			if s.shakeObject(id, 12) {
				roundMoved = true
				changed = true
			}
		}

		if !roundMoved {
			break
		}
	}

	return changed
}

func (s *TileState) overlappingObjectCounts() map[int]int {
	counts := make(map[int]int)
	for _, pt := range s.Polygon.GetWorldTiles() {
		for _, t := range s.Grid.Tiles {
			if t.Constrained {
				continue
			}
			if t.X == pt.X && t.Y == pt.Y {
				counts[t.ObjectID]++
			}
		}
	}
	return counts
}

func (s *TileState) shakeObject(objectID, maxSteps int) bool {
	if maxSteps < 2 {
		maxSteps = 2
	}

	moves := [][2]int{{0, 1}, {1, 0}, {0, -1}, {-1, 0}}
	rand.Shuffle(len(moves), func(i, j int) {
		moves[i], moves[j] = moves[j], moves[i]
	})

	for _, move := range moves {
		steps := 1 + rand.Intn(maxSteps)
		moved := false
		for i := 0; i < steps; i++ {
			if !s.moveObjectIfPossible(objectID, move[0], move[1]) {
				break
			}
			moved = true
		}
		if moved {
			return true
		}
	}

	return false
}

func (s *TileState) randomMacroObjectMove(maxSteps int) bool {
	if maxSteps < 2 {
		maxSteps = 2
	}

	for tries := 0; tries < 24; tries++ {
		idx := rand.Intn(len(s.Grid.Tiles))
		tile := s.Grid.Tiles[idx]
		if tile.Constrained {
			continue
		}

		moves := [][2]int{{0, 1}, {1, 0}, {0, -1}, {-1, 0}}
		rand.Shuffle(len(moves), func(i, j int) {
			moves[i], moves[j] = moves[j], moves[i]
		})

		for _, move := range moves {
			steps := 2 + rand.Intn(maxSteps-1)
			moved := false
			for i := 0; i < steps; i++ {
				if !s.moveObjectIfPossible(tile.ObjectID, move[0], move[1]) {
					break
				}
				moved = true
			}
			if moved {
				return true
			}
		}
	}

	return false
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
		nextID: s.Grid.nextID,
	}

	for i, t := range s.Grid.Tiles {
		newTile := &Tile{
			X:           t.X,
			Y:           t.Y,
			OrigX:       t.OrigX,
			OrigY:       t.OrigY,
			Color:       t.Color,
			Shape:       t.Shape,
			ObjectID:    t.ObjectID,
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
				Shape:       t.Shape,
				ObjectID:    t.ObjectID,
				Constrained: t.Constrained,
			}
		}
	}

	// Deep copy of validPositions queue
	newValidPositions := make(ValidPositionQueue, len(s.validPositions))
	copy(newValidPositions, s.validPositions)
	heap.Init(&newValidPositions)

	newEnergyMemo := make(map[string]float64, len(s.energyMemo))
	for k, v := range s.energyMemo {
		newEnergyMemo[k] = v
	}

	return &TileState{
		Grid:           newGrid,
		Polygon:        newPoly,
		Done:           s.Done,
		validPositions: newValidPositions,
		bestPosition:   s.bestPosition,
		energyMemo:     newEnergyMemo,
	}
}

func (s *TileState) getObjectTiles(objectID int) []*Tile {
	objectTiles := make([]*Tile, 0)
	for _, t := range s.Grid.Tiles {
		if t.ObjectID == objectID {
			objectTiles = append(objectTiles, t)
		}
	}
	return objectTiles
}

func (s *TileState) moveObjectIfPossible(objectID, dx, dy int) bool {
	return s.moveObjectWithFlow(objectID, dx, dy, make(map[int]bool))
}

func (s *TileState) moveObjectWithFlow(objectID, dx, dy int, visiting map[int]bool) bool {
	if dx == 0 && dy == 0 {
		return false
	}
	if visiting[objectID] {
		return false
	}
	visiting[objectID] = true
	defer delete(visiting, objectID)

	objectTiles := s.getObjectTiles(objectID)
	if len(objectTiles) == 0 {
		return false
	}

	blockers := make(map[int]bool)

	for _, tile := range objectTiles {
		newX := tile.X + dx
		newY := tile.Y + dy

		if newX < 0 || newX >= s.Grid.Width || newY < 0 || newY >= s.Grid.Height {
			return false
		}

		for _, other := range s.Grid.Tiles {
			if other.ObjectID == objectID {
				continue
			}
			if other.X == newX && other.Y == newY {
				if other.Constrained {
					return false
				}
				blockers[other.ObjectID] = true
			}
		}
	}

	for blockerID := range blockers {
		if !s.moveObjectWithFlow(blockerID, dx, dy, visiting) {
			return false
		}
	}

	for _, tile := range objectTiles {
		newX := tile.X + dx
		newY := tile.Y + dy
		for _, other := range s.Grid.Tiles {
			if other.ObjectID == objectID {
				continue
			}
			if other.X == newX && other.Y == newY {
				return false
			}
		}
	}

	for _, tile := range objectTiles {
		tile.X += dx
		tile.Y += dy
		tile.Displaced = true
	}

	return true
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
