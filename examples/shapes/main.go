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
	temp           float64
	minTemp        float64
	coolRate       float64
	steps          int
	accepts        int
	improves       int
	bestCost       float64
	movesPerUpdate int
	bestSnapshot   *SceneSnapshot
	done           bool
	lastPrint      int
	lastImprove    int
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

func (s *AnnealState) Move(aggressive bool, freezeTarget bool) bool {
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

	pool := s.movableObjects(freezeTarget)
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

func (s *AnnealState) movableObjects(freezeTarget bool) []*SceneObject {
	pool := make([]*SceneObject, 0, len(s.Objects))
	for _, obj := range s.Objects {
		if !obj.Body.IsMovable {
			continue
		}
		if freezeTarget && obj.IsTarget {
			continue
		}
		pool = append(pool, obj)
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

		for a := 0; a < len(s.Objects); a++ {
			for b := a + 1; b < len(s.Objects); b++ {
				resolveCollision(s.Objects[a].Body, s.Objects[b].Body)
			}
		}

		for _, obj := range s.Objects {
			for _, wall := range s.Walls {
				resolveCollision(obj.Body, wall)
			}
			if obj.IsSoft && obj.Soft != nil {
				for _, node := range obj.Soft.Nodes {
					for _, other := range s.Objects {
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

	for i := 0; i < g.movesPerUpdate; i++ {
		g.steps++
		prevEnergy := g.state.Energy()
		prevOverlap := g.state.overlapCountForTarget()
		baseSnap := g.state.Snapshot()

		aggressive := prevOverlap > 0 || (g.steps-g.lastImprove > 1500)
		proposalCount := 1
		if aggressive {
			proposalCount = 5
		}

		accepted := false
		bestSnap := baseSnap
		bestEnergy := prevEnergy
		bestOverlap := prevOverlap

		for p := 0; p < proposalCount; p++ {
			g.state.Restore(baseSnap)
			freezeTarget := prevOverlap > 0
			if !g.state.Move(aggressive, freezeTarget) {
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
			continue
		}

		g.state.Restore(bestSnap)
		g.accepts++
		if bestEnergy < g.bestCost {
			g.improves++
			g.bestCost = bestEnergy
			g.bestSnapshot = g.state.Snapshot()
			g.lastImprove = g.steps
		}
	}

	if g.steps-g.lastPrint >= 100 {
		overlap := g.state.overlapCountForTarget()
		fmt.Printf("%d\t%.4f\t%.2f\t%.2f%%\t%.2f%%\t%d\n",
			g.steps,
			g.temp,
			g.state.Energy(),
			100*float64(g.accepts)/math.Max(1, float64(g.steps)),
			100*float64(g.improves)/math.Max(1, float64(g.steps)),
			overlap,
		)
		g.lastPrint = g.steps
	}

	g.temp *= g.coolRate
	if g.temp <= g.minTemp {
		g.done = true
		if g.bestSnapshot != nil {
			g.state.Restore(g.bestSnapshot)
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
	drawObject(screen, g.state.Target, true)

	ebitenutil.DebugPrint(screen, fmt.Sprintf("Energy: %.1f  Overlap: %d", g.state.Energy(), g.state.overlapCountForTarget()))
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
		temp:           82,
		minTemp:        0.6,
		coolRate:       0.99935,
		movesPerUpdate: 180,
		bestCost:       scene.Energy(),
		bestSnapshot:   scene.Snapshot(),
		lastImprove:    0,
	}

	fmt.Printf("\nStep\tTemp\tEnergy\tAccepts\tImproves\tOverlap\n")

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

	target := &SceneObject{
		ID:       nextID,
		Kind:     ShapeIncoming,
		Color:    Blue,
		Body:     &rigidbody.RigidBody{Position: vector.Vector{X: 160, Y: 160}, Velocity: vector.Vector{X: 0, Y: 0}, Mass: 80, Shape: "Rectangle", Width: 270, Height: 270, IsMovable: true},
		Original: vector.Vector{X: 160, Y: 160},
		IsTarget: true,
	}
	objects = append(objects, target)

	return &AnnealState{Objects: objects, Target: target, Walls: walls, WidthPx: width, HeightPx: height}
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
