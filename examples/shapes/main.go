package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/rudransh61/Physix-go/dynamics/collision"
	physix "github.com/rudransh61/Physix-go/dynamics/physics"
	"github.com/rudransh61/Physix-go/pkg/rigidbody"
	"github.com/rudransh61/Physix-go/pkg/spring"
	"github.com/rudransh61/Physix-go/pkg/vector"
)

const physixHardInvalidEnergy = 1e12

type SceneObject struct {
	ID       int
	Kind     ShapeKind
	Color    Color
	Body     *rigidbody.RigidBody
	Original vector.Vector
	IsTarget bool
	IsSoft   bool
	Soft     *SoftBody
}

type SoftBody struct {
	Nodes         []*rigidbody.RigidBody
	Springs       []*spring.Spring
	AnchorOffsets []vector.Vector
}

type SceneSnapshot struct {
	Objects []BodySnapshot
}

type BodySnapshot struct {
	Pos     vector.Vector
	Vel     vector.Vector
	NodePos []vector.Vector
	NodeVel []vector.Vector
}

type AnnealState struct {
	Objects  []*SceneObject
	Target   *SceneObject
	Walls    []*rigidbody.RigidBody
	WidthPx  float64
	HeightPx float64
}

type Game struct {
	state          *AnnealState
	startTemp      float64
	temp           float64
	minTemp        float64
	coolRate       float64
	steps          int
	totalSteps     int
	accepts        int
	improves       int
	bestCost       float64
	movesPerUpdate int
	bestSnapshot   *SceneSnapshot
	done           bool
	lastPrint      int
	lastImprove    int
	nextObjectID   int
	rounds         int
	inserted       int
	failStreak     int
	maxFailStreak  int
	maxInsertions  int
	hotTTL         map[int]int
	eliteArchive   []EliteState
	stallSteps     int
}

type EliteState struct {
	Snapshot *SceneSnapshot
	Cost     float64
	Centroid vector.Vector
}

func (s *AnnealState) Snapshot() *SceneSnapshot {
	ss := &SceneSnapshot{Objects: make([]BodySnapshot, len(s.Objects))}
	for i, obj := range s.Objects {
		entry := BodySnapshot{Pos: obj.Body.Position, Vel: obj.Body.Velocity}
		if obj.IsSoft && obj.Soft != nil {
			entry.NodePos = make([]vector.Vector, len(obj.Soft.Nodes))
			entry.NodeVel = make([]vector.Vector, len(obj.Soft.Nodes))
			for j, n := range obj.Soft.Nodes {
				entry.NodePos[j] = n.Position
				entry.NodeVel[j] = n.Velocity
			}
		}
		ss.Objects[i] = entry
	}
	return ss
}

func (s *AnnealState) Restore(ss *SceneSnapshot) {
	for i, obj := range s.Objects {
		obj.Body.Position = ss.Objects[i].Pos
		obj.Body.Velocity = ss.Objects[i].Vel
		if obj.IsSoft && obj.Soft != nil {
			for j, n := range obj.Soft.Nodes {
				n.Position = ss.Objects[i].NodePos[j]
				n.Velocity = ss.Objects[i].NodeVel[j]
			}
		}
	}
}

func (s *AnnealState) overlapCountForTarget() int {
	if s.Target == nil || s.Target.Body == nil {
		return 0
	}
	overlap := 0
	for _, obj := range s.Objects {
		if obj.IsTarget {
			continue
		}
		hit, _ := objectTargetOverlap(obj, s.Target.Body)
		if hit {
			overlap++
		}
	}
	for _, w := range s.Walls {
		if collides(s.Target.Body, w) {
			overlap += 3
		}
	}
	return overlap
}

func (s *AnnealState) Energy() float64 {
	if s.Target == nil || s.Target.Body == nil {
		return 0
	}
	if s.overlapsWall(s.Target.Body) {
		return physixHardInvalidEnergy
	}

	overlapPenalty := 0.0
	overlapAreaSum := 0.0
	overlapCount := 0.0
	for _, obj := range s.Objects {
		if obj.IsTarget {
			continue
		}
		hit, area := objectTargetOverlap(obj, s.Target.Body)
		if hit {
			overlapCount += 1
			overlapAreaSum += area
		}
	}
	overlapPenalty += overlapAreaSum*180.0 + overlapCount*12000.0

	distPenalty := 0.0
	movedObjs := 0.0
	for _, obj := range s.Objects {
		if obj.IsTarget {
			continue
		}
		d := vector.Distance(obj.Original, obj.Body.Position)
		distPenalty += d * 120
		if d > 1 {
			movedObjs += 1
		}
	}

	return overlapPenalty + distPenalty + movedObjs*220
}

func (s *AnnealState) Move(aggressive bool, freezeTarget bool, active map[int]int) bool {
	blockers := s.getBlockingObjects()
	if len(blockers) > 0 && rand.Float64() < 0.82 {
		obj := blockers[rand.Intn(len(blockers))]
		dx, dy := awayVector(obj.Body.Position, s.Target.Body.Position)
		bursts := 1
		if aggressive {
			bursts = 2 + rand.Intn(3)
		}
		for i := 0; i < bursts; i++ {
			step := 12 + rand.Float64()*40
			if aggressive {
				step = 20 + rand.Float64()*70
			}
			obj.Body.Position.X += dx * step
			obj.Body.Position.Y += dy * step
		}
		if aggressive {
			s.PhysicsRelax(5)
		} else {
			s.PhysicsRelax(3)
		}
		return true
	}

	pool := s.movableObjects(freezeTarget, active)
	if len(pool) == 0 {
		return false
	}
	obj := pool[rand.Intn(len(pool))]
	if !obj.Body.IsMovable {
		return false
	}

	step := 6 + rand.Float64()*28
	if aggressive {
		step = 14 + rand.Float64()*60
	}
	ang := rand.Float64() * 2 * math.Pi
	obj.Body.Position.X += math.Cos(ang) * step
	obj.Body.Position.Y += math.Sin(ang) * step
	if aggressive {
		s.PhysicsRelax(5)
	} else {
		s.PhysicsRelax(3)
	}
	return true
}

func (s *AnnealState) SweepClear(aggressive bool) bool {
	if s.Target == nil || s.Target.Body == nil {
		return false
	}

	base := s.Target.Original
	s.Target.Body.Position = base
	s.PhysicsRelax(2)

	blockers := s.getBlockingObjects()
	if len(blockers) == 0 {
		return false
	}

	dirX, dirY := sweepDirectionFromBlockers(blockers, s.Target.Body.Position)
	if math.Hypot(dirX, dirY) < 1e-6 {
		a := rand.Float64() * 2 * math.Pi
		dirX, dirY = math.Cos(a), math.Sin(a)
	}

	sweepDist := 64.0
	if s.Target.Body.Shape == "Rectangle" {
		sweepDist = 0.34 * math.Min(s.Target.Body.Width, s.Target.Body.Height)
	} else if s.Target.Body.Shape == "Circle" {
		sweepDist = 1.1 * s.Target.Body.Radius
	}
	if aggressive {
		sweepDist *= 1.6
	}

	outSteps := 8
	relaxPerStep := 3
	if aggressive {
		outSteps = 12
		relaxPerStep = 4
	}

	for i := 1; i <= outSteps; i++ {
		f := float64(i) / float64(outSteps)
		s.Target.Body.Position.X = base.X + dirX*sweepDist*f
		s.Target.Body.Position.Y = base.Y + dirY*sweepDist*f
		s.PhysicsRelax(relaxPerStep)
	}

	perpX, perpY := -dirY, dirX
	sideDist := sweepDist * 0.45
	if aggressive {
		sideDist = sweepDist * 0.7
	}
	for _, side := range []float64{1, -1} {
		for i := 1; i <= outSteps/2; i++ {
			f := float64(i) / float64(outSteps/2)
			s.Target.Body.Position.X = base.X + dirX*sweepDist + perpX*side*sideDist*f
			s.Target.Body.Position.Y = base.Y + dirY*sweepDist + perpY*side*sideDist*f
			s.PhysicsRelax(relaxPerStep)
		}
		for i := outSteps / 2; i >= 0; i-- {
			f := float64(i) / float64(outSteps/2)
			s.Target.Body.Position.X = base.X + dirX*sweepDist + perpX*side*sideDist*f
			s.Target.Body.Position.Y = base.Y + dirY*sweepDist + perpY*side*sideDist*f
			s.PhysicsRelax(relaxPerStep)
		}
	}

	for i := outSteps; i >= 0; i-- {
		f := float64(i) / float64(outSteps)
		s.Target.Body.Position.X = base.X + dirX*sweepDist*f
		s.Target.Body.Position.Y = base.Y + dirY*sweepDist*f
		s.PhysicsRelax(relaxPerStep)
	}

	s.Target.Body.Position = base
	if aggressive {
		s.PhysicsRelax(7)
	} else {
		s.PhysicsRelax(5)
	}

	return true
}

func sweepDirectionFromBlockers(blockers []*SceneObject, targetPos vector.Vector) (float64, float64) {
	if len(blockers) == 0 {
		return 0, 0
	}

	cx, cy := 0.0, 0.0
	for _, b := range blockers {
		cx += b.Body.Position.X
		cy += b.Body.Position.Y
	}
	cx /= float64(len(blockers))
	cy /= float64(len(blockers))

	dx := cx - targetPos.X
	dy := cy - targetPos.Y
	m := math.Hypot(dx, dy)
	if m < 1e-6 {
		return 0, 0
	}
	return dx / m, dy / m
}

func (s *AnnealState) movableObjects(freezeTarget bool, active map[int]int) []*SceneObject {
	pool := make([]*SceneObject, 0, len(s.Objects))
	activeOnly := len(active) > 0
	for _, obj := range s.Objects {
		if !obj.Body.IsMovable {
			continue
		}
		if freezeTarget && obj.IsTarget {
			continue
		}
		if activeOnly {
			if _, ok := active[obj.ID]; !ok {
				continue
			}
		}
		pool = append(pool, obj)
	}
	if len(pool) == 0 && activeOnly {
		return s.movableObjects(freezeTarget, nil)
	}
	return pool
}

func (s *AnnealState) getBlockingObjects() []*SceneObject {
	blockers := make([]*SceneObject, 0)
	for _, obj := range s.Objects {
		if obj.IsTarget {
			continue
		}
		hit, _ := objectTargetOverlap(obj, s.Target.Body)
		if hit {
			blockers = append(blockers, obj)
		}
	}
	return blockers
}

func (s *AnnealState) overlapsWall(body *rigidbody.RigidBody) bool {
	for _, w := range s.Walls {
		if collides(body, w) {
			return true
		}
	}
	return false
}

func (s *AnnealState) PhysicsRelax(substeps int) {
	for i := 0; i < substeps; i++ {
		for _, obj := range s.Objects {
			if obj.IsSoft && obj.Soft != nil {
				for _, sp := range obj.Soft.Springs {
					sp.ApplyForce()
				}
				for idx, node := range obj.Soft.Nodes {
					off := obj.Soft.AnchorOffsets[idx]
					target := vector.Vector{X: obj.Body.Position.X + off.X, Y: obj.Body.Position.Y + off.Y}
					force := target.Sub(node.Position).Scale(7).Sub(node.Velocity.Scale(2.8))
					physix.ApplyForce(node, force, 0.03)
				}
			}
		}

		grid := buildSpatialHash(s.Objects, 96)
		pairs := grid.candidatePairs()
		for _, p := range pairs {
			resolveCollision(s.Objects[p[0]].Body, s.Objects[p[1]].Body)
		}

		for _, obj := range s.Objects {
			for _, wall := range s.Walls {
				resolveCollision(obj.Body, wall)
			}
			if obj.IsSoft && obj.Soft != nil {
				for _, node := range obj.Soft.Nodes {
					near := grid.nearbyIndicesForBody(node)
					for _, idx := range near {
						other := s.Objects[idx]
						if other == obj {
							continue
						}
						resolveCollision(node, other.Body)
					}
					for _, wall := range s.Walls {
						resolveCollision(node, wall)
					}
				}
			}
		}
	}
}

func objectTargetOverlap(obj *SceneObject, target *rigidbody.RigidBody) (bool, float64) {
	hit := false
	area := 0.0

	if collides(target, obj.Body) {
		hit = true
		area += overlapAreaBodies(target, obj.Body)
	}

	if !obj.IsSoft || obj.Soft == nil {
		return hit, area
	}

	for _, n := range obj.Soft.Nodes {
		if collides(target, n) {
			hit = true
			area += overlapAreaBodies(target, n)
		}
	}

	if target.Shape == "Rectangle" {
		for _, sp := range obj.Soft.Springs {
			if lineIntersectsRect(sp.BallA.Position, sp.BallB.Position, target) {
				hit = true
				area += 380.0
			}
		}
	}

	return hit, area
}

func lineIntersectsRect(a, b vector.Vector, r *rigidbody.RigidBody) bool {
	if r.Shape != "Rectangle" {
		return false
	}
	rx1, ry1 := r.Position.X, r.Position.Y
	rx2, ry2 := r.Position.X+r.Width, r.Position.Y+r.Height

	if pointInRect(a, rx1, ry1, rx2, ry2) || pointInRect(b, rx1, ry1, rx2, ry2) {
		return true
	}

	edges := [][2]vector.Vector{
		{{X: rx1, Y: ry1}, {X: rx2, Y: ry1}},
		{{X: rx2, Y: ry1}, {X: rx2, Y: ry2}},
		{{X: rx2, Y: ry2}, {X: rx1, Y: ry2}},
		{{X: rx1, Y: ry2}, {X: rx1, Y: ry1}},
	}

	for _, e := range edges {
		if segmentsIntersect(a, b, e[0], e[1]) {
			return true
		}
	}
	return false
}

func pointInRect(p vector.Vector, x1, y1, x2, y2 float64) bool {
	return p.X >= x1 && p.X <= x2 && p.Y >= y1 && p.Y <= y2
}

func segmentsIntersect(p1, p2, q1, q2 vector.Vector) bool {
	orient := func(a, b, c vector.Vector) float64 {
		return (b.Y-a.Y)*(c.X-b.X) - (b.X-a.X)*(c.Y-b.Y)
	}
	onSegment := func(a, b, c vector.Vector) bool {
		return b.X <= math.Max(a.X, c.X) && b.X >= math.Min(a.X, c.X) &&
			b.Y <= math.Max(a.Y, c.Y) && b.Y >= math.Min(a.Y, c.Y)
	}

	o1 := orient(p1, p2, q1)
	o2 := orient(p1, p2, q2)
	o3 := orient(q1, q2, p1)
	o4 := orient(q1, q2, p2)

	if o1*o2 < 0 && o3*o4 < 0 {
		return true
	}

	if math.Abs(o1) < 1e-9 && onSegment(p1, q1, p2) {
		return true
	}
	if math.Abs(o2) < 1e-9 && onSegment(p1, q2, p2) {
		return true
	}
	if math.Abs(o3) < 1e-9 && onSegment(q1, p1, q2) {
		return true
	}
	if math.Abs(o4) < 1e-9 && onSegment(q1, p2, q2) {
		return true
	}

	return false
}

func (g *Game) Update() error {
	if g.done {
		return nil
	}
	if g.state.Target == nil {
		if !g.startNextInsertion() {
			g.done = true
		}
		return nil
	}

	moveBudget := g.dynamicMoveBudget()
	for i := 0; i < moveBudget; i++ {
		g.decayHotSet()
		g.steps++
		g.totalSteps++
		prevEnergy := g.state.Energy()
		prevOverlap := g.state.overlapCountForTarget()
		baseSnap := g.state.Snapshot()
		g.markBlockersHot(5)

		aggressive := prevOverlap > 0 || (g.steps-g.lastImprove > 1500)
		proposalCount := g.dynamicProposalCount(aggressive)
		active := g.currentActiveSet(prevOverlap)

		accepted := false
		bestSnap := baseSnap
		bestEnergy := prevEnergy
		bestOverlap := prevOverlap

		for p := 0; p < proposalCount; p++ {
			g.state.Restore(baseSnap)
			moved := false
			if prevOverlap > 0 && (aggressive || rand.Float64() < 0.6) {
				moved = g.state.SweepClear(aggressive)
			}
			if !moved {
				freezeTarget := prevOverlap > 0
				moved = g.state.Move(aggressive, freezeTarget, active)
			}
			if !moved {
				continue
			}

			settleSteps := 2
			if aggressive {
				settleSteps += 4
			}
			if prevOverlap > 0 {
				settleSteps += 4
			}
			g.state.PhysicsRelax(settleSteps)

			newEnergy := g.state.Energy()
			newOverlap := g.state.overlapCountForTarget()
			delta := newEnergy - prevEnergy

			effectiveTemp := g.temp
			if aggressive {
				effectiveTemp = g.temp * 2.4
			}

			accept := false
			if newEnergy >= physixHardInvalidEnergy {
				accept = false
			} else if newOverlap < prevOverlap {
				accept = true
			} else if delta <= 0 {
				accept = true
			} else {
				accept = math.Exp(-delta/effectiveTemp) > rand.Float64()
			}

			if !accept {
				continue
			}

			accepted = true
			if newOverlap < bestOverlap || (newOverlap == bestOverlap && newEnergy < bestEnergy) {
				bestEnergy = newEnergy
				bestOverlap = newOverlap
				bestSnap = g.state.Snapshot()
			}
		}

		if !accepted {
			g.state.Restore(baseSnap)
			g.stallSteps++
			if g.stallSteps > 260 {
				g.tryRestoreElite()
				g.unlockLocalCluster()
				g.stallSteps = 0
			}
			continue
		}

		g.state.Restore(bestSnap)
		g.stallSteps = 0
		g.accepts++
		if bestOverlap == 0 {
			g.recordElite(bestSnap, bestEnergy)
		}
		if bestEnergy < g.bestCost {
			g.improves++
			g.bestCost = bestEnergy
			g.bestSnapshot = g.state.Snapshot()
			g.lastImprove = g.steps
		}
	}

	if g.steps-g.lastPrint >= 100 {
		overlap := g.state.overlapCountForTarget()
		fmt.Printf("R%d\t%d\t%.4f\t%.2f\t%.2f%%\t%.2f%%\t%d\tE%d\n",
			g.rounds,
			g.steps,
			g.temp,
			g.state.Energy(),
			100*float64(g.accepts)/math.Max(1, float64(g.steps)),
			100*float64(g.improves)/math.Max(1, float64(g.steps)),
			overlap,
			len(g.eliteArchive),
		)
		g.lastPrint = g.steps
	}

	g.temp *= g.coolRate
	if g.temp <= g.minTemp {
		if g.bestSnapshot != nil {
			g.state.Restore(g.bestSnapshot)
		}

		success := g.state.overlapCountForTarget() == 0 && !g.state.overlapsWall(g.state.Target.Body)
		if success {
			g.commitCurrentTarget()
			g.inserted++
			g.failStreak = 0
			fmt.Printf("Inserted #%d: %s\n", g.inserted, shapeKindName(g.state.Objects[len(g.state.Objects)-1].Kind))
		} else {
			g.removeCurrentTarget()
			g.failStreak++
			fmt.Printf("Insertion failed (streak %d/%d)\n", g.failStreak, g.maxFailStreak)
		}

		if g.inserted >= g.maxInsertions || g.failStreak >= g.maxFailStreak {
			g.done = true
			g.state.Target = nil
			fmt.Printf("Done: inserted %d objects in %d rounds (%d total steps).\n", g.inserted, g.rounds, g.totalSteps)
			return nil
		}

		if !g.startNextInsertion() {
			g.done = true
			fmt.Printf("Done: inserted %d objects in %d rounds (%d total steps).\n", g.inserted, g.rounds, g.totalSteps)
			return nil
		}
	}

	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	drawerFill := color.RGBA{38, 28, 20, 255}
	drawerBorder := color.RGBA{170, 120, 75, 255}
	ebitenutil.DrawRect(screen, 0, 0, g.state.WidthPx, g.state.HeightPx, drawerFill)
	for _, w := range g.state.Walls {
		ebitenutil.DrawRect(screen, w.Position.X, w.Position.Y, w.Width, w.Height, drawerBorder)
	}

	for _, obj := range g.state.Objects {
		if obj.IsTarget {
			continue
		}
		drawObject(screen, obj, false)
	}
	if g.state.Target != nil {
		drawObject(screen, g.state.Target, true)
		ebitenutil.DebugPrint(screen, fmt.Sprintf("Inserted: %d  Round: %d  Overlap: %d  Temp: %.2f", g.inserted, g.rounds, g.state.overlapCountForTarget(), g.temp))
	} else {
		ebitenutil.DebugPrint(screen, fmt.Sprintf("Drawer full-ish. Inserted: %d  Steps: %d", g.inserted, g.totalSteps))
	}
}

func drawObject(screen *ebiten.Image, obj *SceneObject, target bool) {
	if obj.Body.Shape == "Circle" {
		ebitenutil.DrawCircle(screen, obj.Body.Position.X, obj.Body.Position.Y, obj.Body.Radius, sceneColor(obj.Color, target))
		return
	}

	x := obj.Body.Position.X
	y := obj.Body.Position.Y
	w := obj.Body.Width
	h := obj.Body.Height

	switch obj.Kind {
	case ShapeStar:
		drawStar(screen, x, y, w, h, sceneColor(obj.Color, target))
	case ShapeCylinder:
		drawCylinder(screen, x, y, w, h, sceneColor(obj.Color, target))
	default:
		ebitenutil.DrawRect(screen, x, y, w, h, sceneColor(obj.Color, target))
	}
}

func (g *Game) Layout(w, h int) (int, int) {
	return int(g.state.WidthPx), int(g.state.HeightPx)
}

func main() {
	rand.Seed(time.Now().UnixNano())

	scene := buildScene(600, 600)
	game := &Game{
		state:          scene,
		startTemp:      82,
		temp:           82,
		minTemp:        81,
		coolRate:       0.99935,
		movesPerUpdate: 180,
		nextObjectID:   nextObjectID(scene.Objects),
		maxFailStreak:  4,
		maxInsertions:  120,
		hotTTL:         make(map[int]int),
		lastImprove:    0,
	}
	game.startNextInsertion()

	fmt.Printf("\nRound\tStep\tTemp\tEnergy\tAccepts\tImproves\tOverlap\tElites\n")

	ebiten.SetWindowSize(int(scene.WidthPx), int(scene.HeightPx))
	ebiten.SetWindowTitle("Physix Drawer Annealing")
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}

func buildScene(width, height float64) *AnnealState {
	walls := []*rigidbody.RigidBody{
		{Position: vector.Vector{X: 0, Y: 0}, Shape: "Rectangle", Width: width, Height: 8, IsMovable: false, Mass: rigidbody.Infinite_mass},
		{Position: vector.Vector{X: 0, Y: height - 8}, Shape: "Rectangle", Width: width, Height: 8, IsMovable: false, Mass: rigidbody.Infinite_mass},
		{Position: vector.Vector{X: 0, Y: 0}, Shape: "Rectangle", Width: 8, Height: height, IsMovable: false, Mass: rigidbody.Infinite_mass},
		{Position: vector.Vector{X: width - 8, Y: 0}, Shape: "Rectangle", Width: 8, Height: height, IsMovable: false, Mass: rigidbody.Infinite_mass},
	}

	objects := make([]*SceneObject, 0)
	nextID := 1

	spawnRect := func(kind ShapeKind, c Color, w, h float64, soft bool, count int) {
		for i := 0; i < count; i++ {
			placed := false
			for tries := 0; tries < 1200; tries++ {
				x := 12 + rand.Float64()*(width-w-24)
				y := 12 + rand.Float64()*(height-h-24)
				body := &rigidbody.RigidBody{Position: vector.Vector{X: x, Y: y}, Velocity: vector.Vector{X: 0, Y: 0}, Mass: 50, Shape: "Rectangle", Width: w, Height: h, IsMovable: true}
				if collidesAny(body, objects, walls) {
					continue
				}
				obj := &SceneObject{ID: nextID, Kind: kind, Color: c, Body: body, Original: body.Position, IsSoft: soft}
				nextID++
				if soft {
					obj.Soft = makeSoftBody(body)
				}
				objects = append(objects, obj)
				placed = true
				break
			}
			if !placed {
				fmt.Printf("warning: could not place %v object %d\n", kind, i+1)
			}
		}
	}

	spawnCircle := func(kind ShapeKind, c Color, r float64, soft bool, count int) {
		for i := 0; i < count; i++ {
			placed := false
			for tries := 0; tries < 1200; tries++ {
				x := 12 + r + rand.Float64()*(width-2*r-24)
				y := 12 + r + rand.Float64()*(height-2*r-24)
				body := &rigidbody.RigidBody{Position: vector.Vector{X: x, Y: y}, Velocity: vector.Vector{X: 0, Y: 0}, Mass: 50, Shape: "Circle", Radius: r, IsMovable: true}
				if collidesAny(body, objects, walls) {
					continue
				}
				obj := &SceneObject{ID: nextID, Kind: kind, Color: c, Body: body, Original: body.Position, IsSoft: soft}
				nextID++
				if soft {
					obj.Soft = makeSoftBody(body)
				}
				objects = append(objects, obj)
				placed = true
				break
			}
			if !placed {
				fmt.Printf("warning: could not place %v object %d\n", kind, i+1)
			}
		}
	}

	spawnRect(ShapeStar, Yellow, 95, 95, true, 3)
	spawnRect(ShapeSquare, Blue, 90, 90, false, 4)
	spawnCircle(ShapeCylinder, Green, 42, true, 3)
	spawnRect(ShapeRectangle, Blue, 140, 60, false, 4)

	return &AnnealState{Objects: objects, Target: nil, Walls: walls, WidthPx: width, HeightPx: height}
}

func (g *Game) startNextInsertion() bool {
	if g.inserted >= g.maxInsertions {
		return false
	}
	target := randomIncomingObject(g.nextObjectID, g.state.WidthPx, g.state.HeightPx)
	seedTargetFromAnchors(g.state, target)
	g.nextObjectID++
	g.state.Target = target
	g.state.Objects = append(g.state.Objects, target)
	g.rounds++
	g.resetRound()
	return true
}

func (g *Game) resetRound() {
	g.temp = g.startTemp
	g.steps = 0
	g.accepts = 0
	g.improves = 0
	g.lastPrint = 0
	g.lastImprove = 0
	g.stallSteps = 0
	g.hotTTL = make(map[int]int)
	g.eliteArchive = g.eliteArchive[:0]
	g.bestCost = g.state.Energy()
	g.bestSnapshot = g.state.Snapshot()
}

func (g *Game) commitCurrentTarget() {
	if g.state.Target == nil {
		return
	}
	g.state.Target.IsTarget = false
	g.state.Target.Original = g.state.Target.Body.Position
	g.state.Target = nil
}

func (g *Game) removeCurrentTarget() {
	if g.state.Target == nil {
		return
	}
	t := g.state.Target
	filtered := make([]*SceneObject, 0, len(g.state.Objects)-1)
	for _, obj := range g.state.Objects {
		if obj != t {
			filtered = append(filtered, obj)
		}
	}
	g.state.Objects = filtered
	g.state.Target = nil
}

func nextObjectID(objs []*SceneObject) int {
	maxID := 0
	for _, obj := range objs {
		if obj.ID > maxID {
			maxID = obj.ID
		}
	}
	return maxID + 1
}

func randomIncomingObject(id int, width, height float64) *SceneObject {
	kind := randomInsertKind()
	clr := randomInsertColor()

	body := &rigidbody.RigidBody{Velocity: vector.Vector{X: 0, Y: 0}, Mass: 70, IsMovable: true}

	switch kind {
	case ShapeSquare:
		sz := 64 + rand.Float64()*64
		body.Shape = "Rectangle"
		body.Width = sz
		body.Height = sz
		body.Position = vector.Vector{X: 12 + rand.Float64()*(width-sz-24), Y: 12 + rand.Float64()*(height-sz-24)}
	case ShapeRectangle:
		w := 90 + rand.Float64()*120
		h := 44 + rand.Float64()*72
		body.Shape = "Rectangle"
		body.Width = w
		body.Height = h
		body.Position = vector.Vector{X: 12 + rand.Float64()*(width-w-24), Y: 12 + rand.Float64()*(height-h-24)}
	case ShapeStar:
		sz := 70 + rand.Float64()*70
		body.Shape = "Rectangle"
		body.Width = sz
		body.Height = sz
		body.Position = vector.Vector{X: 12 + rand.Float64()*(width-sz-24), Y: 12 + rand.Float64()*(height-sz-24)}
	case ShapeCylinder:
		r := 26 + rand.Float64()*34
		body.Shape = "Circle"
		body.Radius = r
		body.Position = vector.Vector{X: 12 + r + rand.Float64()*(width-2*r-24), Y: 12 + r + rand.Float64()*(height-2*r-24)}
	default:
		sz := 72 + rand.Float64()*68
		body.Shape = "Rectangle"
		body.Width = sz
		body.Height = sz
		body.Position = vector.Vector{X: 12 + rand.Float64()*(width-sz-24), Y: 12 + rand.Float64()*(height-sz-24)}
	}

	return &SceneObject{
		ID:       id,
		Kind:     kind,
		Color:    clr,
		Body:     body,
		Original: body.Position,
		IsTarget: true,
	}
}

func randomInsertKind() ShapeKind {
	kinds := []ShapeKind{ShapeStar, ShapeSquare, ShapeCylinder, ShapeRectangle}
	return kinds[rand.Intn(len(kinds))]
}

func randomInsertColor() Color {
	colors := []Color{Blue, Green, Yellow, Red}
	return colors[rand.Intn(len(colors))]
}

func (g *Game) dynamicMoveBudget() int {
	budget := g.movesPerUpdate
	n := len(g.state.Objects)
	switch {
	case n > 140:
		budget = int(float64(budget) * 0.25)
	case n > 100:
		budget = int(float64(budget) * 0.35)
	case n > 70:
		budget = int(float64(budget) * 0.5)
	case n > 45:
		budget = int(float64(budget) * 0.7)
	}
	if budget < 24 {
		budget = 24
	}
	return budget
}

func (g *Game) dynamicProposalCount(aggressive bool) int {
	if !aggressive {
		return 1
	}
	n := len(g.state.Objects)
	switch {
	case n > 120:
		return 2
	case n > 70:
		return 3
	default:
		return 5
	}
}

func (g *Game) decayHotSet() {
	for id, ttl := range g.hotTTL {
		ttl--
		if ttl <= 0 {
			delete(g.hotTTL, id)
			continue
		}
		g.hotTTL[id] = ttl
	}
}

func (g *Game) markBlockersHot(ttl int) {
	if g.state.Target == nil {
		return
	}
	for _, obj := range g.state.getBlockingObjects() {
		g.hotTTL[obj.ID] = ttl
	}
	near := nearbyObjectsToTarget(g.state, 180)
	for _, obj := range near {
		g.hotTTL[obj.ID] = localMaxInt(g.hotTTL[obj.ID], ttl-1)
	}
}

func (g *Game) currentActiveSet(prevOverlap int) map[int]int {
	if prevOverlap <= 0 {
		return nil
	}
	active := make(map[int]int, len(g.hotTTL))
	for id, ttl := range g.hotTTL {
		if ttl > 0 {
			active[id] = ttl
		}
	}
	if len(active) == 0 {
		for _, obj := range g.state.getBlockingObjects() {
			active[obj.ID] = 3
		}
	}
	return active
}

func (g *Game) unlockLocalCluster() {
	if g.state.Target == nil {
		return
	}
	cluster := nearbyObjectsToTarget(g.state, 220)
	if len(cluster) == 0 {
		return
	}
	n := minInt(len(cluster), 6)
	for i := 0; i < n; i++ {
		obj := cluster[rand.Intn(len(cluster))]
		dx, dy := awayVector(obj.Body.Position, g.state.Target.Body.Position)
		step := 12 + rand.Float64()*24
		obj.Body.Position.X += dx * step
		obj.Body.Position.Y += dy * step
		g.hotTTL[obj.ID] = 6
	}
	g.state.PhysicsRelax(4)
	if len(g.eliteArchive) > 0 && rand.Float64() < 0.35 {
		g.tryRestoreElite()
	}
}

func (g *Game) recordElite(snap *SceneSnapshot, cost float64) {
	if snap == nil || g.state.Target == nil {
		return
	}
	centroid := displacementCentroid(g.state)
	const minEliteSpacing = 18.0

	for i := range g.eliteArchive {
		d := vector.Distance(g.eliteArchive[i].Centroid, centroid)
		if d < minEliteSpacing {
			if cost < g.eliteArchive[i].Cost {
				g.eliteArchive[i] = EliteState{Snapshot: snap, Cost: cost, Centroid: centroid}
			}
			return
		}
	}

	g.eliteArchive = append(g.eliteArchive, EliteState{Snapshot: snap, Cost: cost, Centroid: centroid})
	if len(g.eliteArchive) <= 6 {
		return
	}

	worst := 0
	for i := 1; i < len(g.eliteArchive); i++ {
		if g.eliteArchive[i].Cost > g.eliteArchive[worst].Cost {
			worst = i
		}
	}
	g.eliteArchive = append(g.eliteArchive[:worst], g.eliteArchive[worst+1:]...)
}

func (g *Game) tryRestoreElite() {
	if len(g.eliteArchive) == 0 {
		return
	}
	pick := g.eliteArchive[rand.Intn(len(g.eliteArchive))]
	g.state.Restore(pick.Snapshot)
	g.temp = math.Min(g.startTemp*1.6, math.Max(g.temp, g.startTemp*0.75))
	g.lastImprove = g.steps
}

func seedTargetFromAnchors(state *AnnealState, target *SceneObject) {
	if state == nil || target == nil || target.Body == nil {
		return
	}
	anchors := insertionAnchors(state, target.Body)
	if len(anchors) == 0 {
		return
	}

	orig := target.Body.Position
	bestPos := orig
	bestScore := math.Inf(1)
	for _, p := range anchors {
		target.Body.Position = p
		score := candidatePlacementScore(state, target)
		if score < bestScore {
			bestScore = score
			bestPos = p
		}
	}
	target.Body.Position = bestPos
	target.Original = bestPos
}

func insertionAnchors(state *AnnealState, body *rigidbody.RigidBody) []vector.Vector {
	anchors := make([]vector.Vector, 0, 64)
	margin := 14.0
	for i := 0; i < 5; i++ {
		fx := float64(i) / 4.0
		for j := 0; j < 5; j++ {
			fy := float64(j) / 4.0
			anchors = append(anchors, placementFromTopLeft(state, body, vector.Vector{X: margin + fx*(state.WidthPx-2*margin), Y: margin + fy*(state.HeightPx-2*margin)}))
		}
	}
	for _, obj := range state.Objects {
		if obj.IsTarget {
			continue
		}
		minX, minY, maxX, maxY := bodyAABB(obj.Body)
		cx := (minX + maxX) / 2
		cy := (minY + maxY) / 2
		gap := 16.0
		anchors = append(anchors,
			placementFromTopLeft(state, body, vector.Vector{X: minX - gap, Y: cy}),
			placementFromTopLeft(state, body, vector.Vector{X: maxX + gap, Y: cy}),
			placementFromTopLeft(state, body, vector.Vector{X: cx, Y: minY - gap}),
			placementFromTopLeft(state, body, vector.Vector{X: cx, Y: maxY + gap}),
		)
	}
	return anchors
}

func placementFromTopLeft(state *AnnealState, body *rigidbody.RigidBody, p vector.Vector) vector.Vector {
	if body.Shape == "Circle" {
		r := body.Radius
		x := clamp(p.X, 10+r, state.WidthPx-10-r)
		y := clamp(p.Y, 10+r, state.HeightPx-10-r)
		return vector.Vector{X: x, Y: y}
	}
	x := clamp(p.X, 10, state.WidthPx-10-body.Width)
	y := clamp(p.Y, 10, state.HeightPx-10-body.Height)
	return vector.Vector{X: x, Y: y}
}

func candidatePlacementScore(state *AnnealState, target *SceneObject) float64 {
	if state.overlapsWall(target.Body) {
		return physixHardInvalidEnergy
	}
	score := 0.0
	for _, obj := range state.Objects {
		if obj == target || obj.IsTarget {
			continue
		}
		if collides(target.Body, obj.Body) {
			score += 9000 + overlapAreaBodies(target.Body, obj.Body)*200
		}
	}
	return score
}

func nearbyObjectsToTarget(state *AnnealState, radius float64) []*SceneObject {
	if state == nil || state.Target == nil {
		return nil
	}
	tc := bodyCenter(state.Target.Body)
	near := make([]*SceneObject, 0)
	for _, obj := range state.Objects {
		if obj.IsTarget {
			continue
		}
		if vector.Distance(tc, bodyCenter(obj.Body)) <= radius {
			near = append(near, obj)
		}
	}
	return near
}

func bodyCenter(body *rigidbody.RigidBody) vector.Vector {
	if body.Shape == "Circle" {
		return body.Position
	}
	return vector.Vector{X: body.Position.X + body.Width/2, Y: body.Position.Y + body.Height/2}
}

func displacementCentroid(state *AnnealState) vector.Vector {
	sumX, sumY := 0.0, 0.0
	cnt := 0.0
	for _, obj := range state.Objects {
		if obj.IsTarget {
			continue
		}
		if vector.Distance(obj.Original, obj.Body.Position) < 1 {
			continue
		}
		sumX += obj.Body.Position.X
		sumY += obj.Body.Position.Y
		cnt++
	}
	if cnt == 0 {
		if state.Target != nil {
			return bodyCenter(state.Target.Body)
		}
		return vector.Vector{X: 0, Y: 0}
	}
	return vector.Vector{X: sumX / cnt, Y: sumY / cnt}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func localMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func shapeKindName(kind ShapeKind) string {
	switch kind {
	case ShapeStar:
		return "star"
	case ShapeSquare:
		return "square"
	case ShapeCylinder:
		return "cylinder"
	case ShapeRectangle:
		return "rectangle"
	default:
		return "object"
	}
}

type cellKey struct {
	x int
	y int
}

type spatialHash struct {
	cellSize float64
	buckets  map[cellKey][]int
}

func buildSpatialHash(objs []*SceneObject, cellSize float64) *spatialHash {
	h := &spatialHash{cellSize: cellSize, buckets: make(map[cellKey][]int)}
	for i, obj := range objs {
		h.insert(i, obj.Body)
	}
	return h
}

func (h *spatialHash) insert(idx int, body *rigidbody.RigidBody) {
	minX, minY, maxX, maxY := bodyAABB(body)
	x0 := int(math.Floor(minX / h.cellSize))
	y0 := int(math.Floor(minY / h.cellSize))
	x1 := int(math.Floor(maxX / h.cellSize))
	y1 := int(math.Floor(maxY / h.cellSize))
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			k := cellKey{x: x, y: y}
			h.buckets[k] = append(h.buckets[k], idx)
		}
	}
}

func (h *spatialHash) nearbyIndicesForBody(body *rigidbody.RigidBody) []int {
	minX, minY, maxX, maxY := bodyAABB(body)
	x0 := int(math.Floor(minX / h.cellSize))
	y0 := int(math.Floor(minY / h.cellSize))
	x1 := int(math.Floor(maxX / h.cellSize))
	y1 := int(math.Floor(maxY / h.cellSize))

	seen := make(map[int]struct{})
	near := make([]int, 0)
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for _, idx := range h.buckets[cellKey{x: x, y: y}] {
				if _, ok := seen[idx]; ok {
					continue
				}
				seen[idx] = struct{}{}
				near = append(near, idx)
			}
		}
	}
	return near
}

func (h *spatialHash) candidatePairs() [][2]int {
	seen := make(map[uint64]struct{})
	pairs := make([][2]int, 0)
	for _, ids := range h.buckets {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := ids[i], ids[j]
				if a > b {
					a, b = b, a
				}
				key := (uint64(uint32(a)) << 32) | uint64(uint32(b))
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				pairs = append(pairs, [2]int{a, b})
			}
		}
	}
	return pairs
}

func bodyAABB(body *rigidbody.RigidBody) (float64, float64, float64, float64) {
	if body.Shape == "Circle" {
		return body.Position.X - body.Radius, body.Position.Y - body.Radius,
			body.Position.X + body.Radius, body.Position.Y + body.Radius
	}
	return body.Position.X, body.Position.Y,
		body.Position.X + body.Width, body.Position.Y + body.Height
}

func collidesAny(body *rigidbody.RigidBody, objs []*SceneObject, walls []*rigidbody.RigidBody) bool {
	for _, o := range objs {
		if collides(body, o.Body) {
			return true
		}
	}
	for _, w := range walls {
		if collides(body, w) {
			return true
		}
	}
	return false
}

func resolveCollision(a, b *rigidbody.RigidBody) {
	if !collides(a, b) {
		return
	}

	switch {
	case a.Shape == "Circle" && b.Shape == "Circle":
		collision.PreventCircleOverlap(a, b)
		collision.BounceOnCollision(a, b, 0.25)
	case a.Shape == "Circle" && b.Shape == "Rectangle":
		collision.PreventCircleRectangleOverlap(a, b)
		collision.BounceOnCollision(a, b, 0.2)
	case a.Shape == "Rectangle" && b.Shape == "Circle":
		collision.PreventCircleRectangleOverlap(b, a)
		collision.BounceOnCollision(a, b, 0.2)
	default:
		collision.PreventRectangleOverlap(a, b)
		collision.BounceOnCollision(a, b, 0.15)
	}
}

func collides(a, b *rigidbody.RigidBody) bool {
	switch {
	case a.Shape == "Circle" && b.Shape == "Circle":
		return collision.CircleCollided(a, b)
	case a.Shape == "Circle" && b.Shape == "Rectangle":
		return collision.CircleRectangleCollided(a, b)
	case a.Shape == "Rectangle" && b.Shape == "Circle":
		return collision.CircleRectangleCollided(b, a)
	default:
		return collision.RectangleCollided(a, b)
	}
}

func overlapAreaBodies(a, b *rigidbody.RigidBody) float64 {
	switch {
	case a.Shape == "Rectangle" && b.Shape == "Rectangle":
		return overlapAreaRectRect(a, b)
	case a.Shape == "Circle" && b.Shape == "Circle":
		return overlapAreaCircleCircle(a, b)
	case a.Shape == "Circle" && b.Shape == "Rectangle":
		return overlapAreaCircleRect(a, b)
	case a.Shape == "Rectangle" && b.Shape == "Circle":
		return overlapAreaCircleRect(b, a)
	default:
		return 0
	}
}

func overlapAreaRectRect(a, b *rigidbody.RigidBody) float64 {
	ax1, ay1 := a.Position.X, a.Position.Y
	ax2, ay2 := a.Position.X+a.Width, a.Position.Y+a.Height
	bx1, by1 := b.Position.X, b.Position.Y
	bx2, by2 := b.Position.X+b.Width, b.Position.Y+b.Height

	ox := math.Max(0, math.Min(ax2, bx2)-math.Max(ax1, bx1))
	oy := math.Max(0, math.Min(ay2, by2)-math.Max(ay1, by1))
	return ox * oy
}

func overlapAreaCircleCircle(a, b *rigidbody.RigidBody) float64 {
	d := vector.Distance(a.Position, b.Position)
	overlap := (a.Radius + b.Radius) - d
	if overlap <= 0 {
		return 0
	}
	return math.Pi * overlap * overlap
}

func overlapAreaCircleRect(circle, rect *rigidbody.RigidBody) float64 {
	cx, cy := circle.Position.X, circle.Position.Y
	rx1, ry1 := rect.Position.X, rect.Position.Y
	rx2, ry2 := rect.Position.X+rect.Width, rect.Position.Y+rect.Height

	closestX := clamp(cx, rx1, rx2)
	closestY := clamp(cy, ry1, ry2)
	dx := cx - closestX
	dy := cy - closestY
	dist := math.Hypot(dx, dy)
	penetration := circle.Radius - dist
	if penetration <= 0 {
		return 0
	}
	return math.Pi * penetration * penetration
}

func clamp(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func makeSoftBody(anchor *rigidbody.RigidBody) *SoftBody {
	cx, cy := anchor.Position.X, anchor.Position.Y
	if anchor.Shape == "Rectangle" {
		cx += anchor.Width / 2
		cy += anchor.Height / 2
	}
	rx := 42.0
	ry := 42.0
	if anchor.Shape == "Rectangle" {
		rx = anchor.Width * 0.36
		ry = anchor.Height * 0.36
	}

	offsets := []vector.Vector{{X: -rx, Y: -ry}, {X: rx, Y: -ry}, {X: rx, Y: ry}, {X: -rx, Y: ry}, {X: 0, Y: 0}}
	nodes := make([]*rigidbody.RigidBody, len(offsets))
	for i, off := range offsets {
		nodes[i] = &rigidbody.RigidBody{Position: vector.Vector{X: cx + off.X, Y: cy + off.Y}, Velocity: vector.Vector{X: 0, Y: 0}, Mass: 20, Shape: "Circle", Radius: 5, IsMovable: true}
	}

	springs := []*spring.Spring{
		spring.NewSpring(nodes[0], nodes[1], 3.0, 2.2),
		spring.NewSpring(nodes[1], nodes[2], 3.0, 2.2),
		spring.NewSpring(nodes[2], nodes[3], 3.0, 2.2),
		spring.NewSpring(nodes[3], nodes[0], 3.0, 2.2),
		spring.NewSpring(nodes[0], nodes[2], 2.8, 2.2),
		spring.NewSpring(nodes[1], nodes[3], 2.8, 2.2),
		spring.NewSpring(nodes[4], nodes[0], 3.2, 2.2),
		spring.NewSpring(nodes[4], nodes[1], 3.2, 2.2),
		spring.NewSpring(nodes[4], nodes[2], 3.2, 2.2),
		spring.NewSpring(nodes[4], nodes[3], 3.2, 2.2),
	}

	return &SoftBody{Nodes: nodes, Springs: springs, AnchorOffsets: offsets}
}

func awayVector(a, b vector.Vector) (float64, float64) {
	dx := a.X - b.X
	dy := a.Y - b.Y
	m := math.Hypot(dx, dy)
	if m < 1e-6 {
		a := rand.Float64() * 2 * math.Pi
		return math.Cos(a), math.Sin(a)
	}
	return dx / m, dy / m
}

func sceneColor(c Color, highlight bool) color.Color {
	if highlight {
		return color.RGBA{170, 90, 210, 200}
	}
	switch c {
	case Red:
		return color.RGBA{255, 0, 0, 255}
	case Blue:
		return color.RGBA{40, 120, 255, 255}
	case Green:
		return color.RGBA{50, 220, 110, 255}
	case Yellow:
		return color.RGBA{255, 220, 40, 255}
	default:
		return color.White
	}
}

func drawStar(screen *ebiten.Image, x, y, w, h float64, c color.Color) {
	cx := x + w/2
	cy := y + h/2
	rOuter := math.Min(w, h) * 0.5
	rInner := rOuter * 0.42
	points := make([][2]float64, 10)
	for i := 0; i < 10; i++ {
		ang := -math.Pi/2 + float64(i)*math.Pi/5
		r := rOuter
		if i%2 == 1 {
			r = rInner
		}
		points[i] = [2]float64{cx + math.Cos(ang)*r, cy + math.Sin(ang)*r}
	}
	ebitenutil.DrawRect(screen, cx-rInner*0.8, cy-rInner*0.8, rInner*1.6, rInner*1.6, c)
	for i := 0; i < 10; i++ {
		j := (i + 1) % 10
		ebitenutil.DrawLine(screen, points[i][0], points[i][1], points[j][0], points[j][1], c)
	}
}

func drawCylinder(screen *ebiten.Image, x, y, w, h float64, c color.Color) {
	bodyTop := y + h*0.2
	bodyBottom := y + h*0.8
	ebitenutil.DrawRect(screen, x+2, bodyTop, w-4, bodyBottom-bodyTop, c)
	drawEllipseOutline(screen, x+w/2, bodyTop, w*0.48, h*0.2, color.Black)
	drawEllipseOutline(screen, x+w/2, bodyBottom, w*0.48, h*0.2, color.Black)
	ebitenutil.DrawLine(screen, x+2, bodyTop, x+2, bodyBottom, color.Black)
	ebitenutil.DrawLine(screen, x+w-2, bodyTop, x+w-2, bodyBottom, color.Black)
}

func drawEllipseOutline(screen *ebiten.Image, cx, cy, rx, ry float64, c color.Color) {
	segments := 24
	for i := 0; i < segments; i++ {
		a0 := 2 * math.Pi * float64(i) / float64(segments)
		a1 := 2 * math.Pi * float64(i+1) / float64(segments)
		x0 := cx + math.Cos(a0)*rx
		y0 := cy + math.Sin(a0)*ry
		x1 := cx + math.Cos(a1)*rx
		y1 := cy + math.Sin(a1)*ry
		ebitenutil.DrawLine(screen, x0, y0, x1, y1, c)
	}
}
