'use client'

import { Canvas, useFrame, useThree } from '@react-three/fiber'
import Image from 'next/image'
import {
  Component,
  type ReactNode,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import * as THREE from 'three'

const P = 'P'
const S = 'S'
const E = '.'

type Cell = typeof P | typeof S | typeof E

const FACE: Cell[][][] = [
  [
    [E, E, E],
    [E, E, P],
    [P, P, P],
  ],
  [
    [E, E, E],
    [E, S, E],
    [P, E, E],
  ],
  [
    [P, P, E],
    [P, E, E],
    [P, E, E],
  ],
]

const COLORS = {
  P: '#3d5a80',
  S: '#f8a800',
} as const

const FRUSTUM = 5.2
const CELL = 1.0
const GAP = 0.1
const S_SCALE = 1.25
const SHATTER_HOLD = 1.1
const IMPULSE_STRENGTH = 1.5
const UP = new THREE.Vector3(0.18, 1, 0.32)
const FALLBACK_AXIS = new THREE.Vector3(1, 0, 0)

function easeOutCubic(t: number) {
  return 1 - (1 - t) ** 3
}

function easeOutBack(t: number) {
  const c1 = 1.70158
  const c3 = c1 + 1
  return 1 + c3 * (t - 1) ** 3 + c1 * (t - 1) ** 2
}

type CubeDef = {
  cell: typeof P | typeof S
  start: THREE.Vector3
  target: THREE.Vector3
  explode: THREE.Vector3
  delay: number
  spin: THREE.Euler
  tumble: THREE.Euler
  tumbleSpeed: number
  targetScale: number
  stagger: number
  orbitAxis: THREE.Vector3
  orbitSpeed: number
  orbitPhase: number
}

function buildCubes(): CubeDef[] {
  const cubes: CubeDef[] = []
  let order = 0

  FACE.forEach((layer, y) =>
    layer.forEach((row, z) =>
      row.forEach((cell, x) => {
        if (cell === E) return
        const target = new THREE.Vector3((x - 1) * CELL, (y - 1) * CELL, (z - 1) * CELL)
        const start = target.clone()
        start.x += (x - 1) * 1.8
        start.y += 2.4 + (2 - y) * 0.35
        start.z += (z - 1) * 1.4

        const explode = new THREE.Vector3()
        const orbitAxis = new THREE.Vector3()
        if (cell === S) {
          explode.set(0.28, 0.52, 0.1)
          orbitAxis.set(0.15, 1, 0.08).normalize()
        } else {
          explode.copy(target).normalize()
          explode.multiplyScalar(1.92 + (order % 4) * 0.18)
          explode.y += 0.16 + (y - 1) * 0.1
          orbitAxis.crossVectors(explode, UP)
          if (orbitAxis.lengthSq() < 0.001) orbitAxis.crossVectors(explode, FALLBACK_AXIS)
          orbitAxis.normalize()
          const swirl = new THREE.Vector3().crossVectors(orbitAxis, explode).normalize()
          explode.addScaledVector(swirl, 0.36 + (order % 3) * 0.12)
        }

        const delay = cell === S ? 0.72 : order * 0.07
        const dist = target.length()
        if (cell !== S) order += 1
        cubes.push({
          cell,
          start,
          target,
          explode,
          delay,
          spin: new THREE.Euler((x - 1) * 0.55, 0.35, (y - 1) * -0.4),
          tumble: new THREE.Euler(
            0.7 + (x - 1) * 0.85,
            1.1 + (z - 1) * 0.55,
            -0.65 + (y - 1) * 0.7,
          ),
          tumbleSpeed: 0.85 + (order % 5) * 0.35,
          targetScale: cell === S ? S_SCALE : 1,
          stagger: cell === S ? 0 : 0.05 + dist * 0.1,
          orbitAxis,
          orbitSpeed: cell === S ? 0.55 : 0.42 + (order % 5) * 0.16,
          orbitPhase: order * 0.93 + dist,
        })
      }),
    ),
  )

  return cubes
}

function FitOrthoCamera() {
  const { camera, size } = useThree()

  useLayoutEffect(() => {
    if (!(camera instanceof THREE.OrthographicCamera)) return
    camera.zoom = size.height / (2 * FRUSTUM)
    camera.lookAt(0, 0, 0)
    camera.updateProjectionMatrix()
  }, [camera, size])

  return null
}

function LogoScene({
  hovered,
  entry,
  reduceMotion,
}: {
  hovered: { current: boolean }
  entry: { current: THREE.Vector2 }
  reduceMotion: boolean
}) {
  const groupRef = useRef<THREE.Group>(null)
  const coreLightRef = useRef<THREE.PointLight>(null)
  const meshRefs = useRef<(THREE.Mesh | null)[]>([])
  const cubes = useMemo(buildCubes, [])
  const breakT = useRef(cubes.map(() => 0))
  const breakV = useRef(cubes.map(() => 0))
  const staggers = useRef(cubes.map((cube) => cube.stagger))
  const hoverOnAt = useRef(0)
  const wasHovered = useRef(false)
  const yaw = useRef(0)
  const scratch = useRef({
    assembled: new THREE.Vector3(),
    orbit: new THREE.Vector3(),
    tangent: new THREE.Vector3(),
    impulse: new THREE.Vector3(0, 1, 0),
    camRight: new THREE.Vector3(),
    camUp: new THREE.Vector3(),
  })

  useFrame((state, delta) => {
    const group = groupRef.current
    if (!group || reduceMotion) return

    const dt = Math.min(delta, 0.05)
    const t = state.clock.elapsedTime
    const hovering = hovered.current
    const { assembled, orbit, tangent, impulse, camRight, camUp } = scratch.current
    if (hovering && !wasHovered.current) {
      hoverOnAt.current = t
      // Map the screen-space entry direction into world space using the
      // camera basis, so cubes fly the way the pointer travelled.
      camRight.setFromMatrixColumn(state.camera.matrixWorld, 0)
      camUp.setFromMatrixColumn(state.camera.matrixWorld, 1)
      impulse.copy(camRight).multiplyScalar(entry.current.x).addScaledVector(camUp, entry.current.y)
      if (impulse.lengthSq() < 0.001) impulse.set(0, 1, 0)
      impulse.normalize()
      // Cubes on the side the pointer entered from break first.
      for (let i = 0; i < cubes.length; i++) {
        const along = cubes[i].target.dot(impulse)
        staggers.current[i] = cubes[i].cell === S ? 0.04 : (1.75 - along) * 0.055
      }
    }
    wasHovered.current = hovering
    const hoverAge = hovering ? t - hoverOnAt.current : 0

    let breakAvg = 0

    for (let i = 0; i < cubes.length; i++) {
      const cube = cubes[i]
      const mesh = meshRefs.current[i]
      if (!mesh) continue

      const stagger = staggers.current[i]
      const exploding = hovering && hoverAge >= stagger && hoverAge < SHATTER_HOLD + stagger
      const targetBreak = exploding ? 1 : 0
      const stiffness = exploding ? 18 : 46
      const damping = exploding ? 5.4 : 10.5
      breakV.current[i] += (targetBreak - breakT.current[i]) * stiffness * dt
      breakV.current[i] *= Math.exp(-damping * dt)
      breakT.current[i] += breakV.current[i] * dt
      if (!exploding && breakT.current[i] < 0) {
        breakT.current[i] = 0
        breakV.current[i] = 0
      }
      const shatter = breakT.current[i]
      const mix = THREE.MathUtils.clamp(shatter, 0, 1.22)
      breakAvg += THREE.MathUtils.clamp(shatter, 0, 1)

      const local = THREE.MathUtils.clamp((t - cube.delay) / 0.7, 0, 1)
      const posT = easeOutCubic(local)
      const scaleT = easeOutBack(local)
      assembled.lerpVectors(cube.start, cube.target, posT)

      const theta = t * cube.orbitSpeed + cube.orbitPhase
      orbit.copy(cube.explode).applyAxisAngle(cube.orbitAxis, theta)
      tangent.crossVectors(cube.orbitAxis, cube.explode)
      if (tangent.lengthSq() > 0.0001) {
        tangent.normalize()
        const arc = Math.sin(Math.min(Math.max(mix, 0), 1) * Math.PI)
        orbit.addScaledVector(tangent, arc * 0.55)
      }
      orbit.y += Math.sin(t * 1.8 + cube.orbitPhase) * 0.1 * Math.min(mix, 1)
      // Push the debris cloud along the pointer's entry direction.
      orbit.addScaledVector(
        impulse,
        Math.min(mix, 1) * IMPULSE_STRENGTH * (cube.cell === S ? 0.35 : 1),
      )

      mesh.position.lerpVectors(assembled, orbit, mix)

      const introSpin = 1 - posT
      const debris = Math.min(Math.max(mix, 0), 1)
      // Gentle tilt plus a slow drift while broken (based on time since the
      // break started, so it never spins up with page age).
      const breakAge = Math.max(0, t - hoverOnAt.current)
      const spin = debris * (0.3 + Math.min(breakAge, 2.5) * cube.tumbleSpeed * 0.12)
      mesh.rotation.set(
        cube.spin.x * introSpin + cube.tumble.x * spin,
        cube.spin.y * introSpin + cube.tumble.y * spin,
        cube.spin.z * introSpin + cube.tumble.z * spin,
      )

      const corePulse = cube.cell === S ? 1 + debris * 0.16 * (1 + 0.45 * Math.sin(t * 5.2)) : 1
      const flyPunch = 1 + Math.sin(Math.min(Math.max(mix, 0), 1) * Math.PI) * 0.1
      mesh.scale.setScalar(Math.max(0.001, scaleT * cube.targetScale * corePulse * flyPunch))

      const material = mesh.material
      if (cube.cell === S && material instanceof THREE.MeshLambertMaterial) {
        material.emissive.set(COLORS.S)
        material.emissiveIntensity = debris * 0.55
      }
    }

    breakAvg /= cubes.length
    const idle = Math.max(0, t - 1.35)
    const intact = 1 - breakAvg
    if (breakAvg > 0.02) yaw.current += breakAvg * 0.2 * dt
    else yaw.current = THREE.MathUtils.damp(yaw.current, 0, 2.4, dt)
    group.rotation.y = Math.sin(idle * 0.55) * 0.32 * intact + yaw.current
    group.rotation.x = Math.sin(idle * 0.37) * 0.07 * intact + breakAvg * 0.1 * Math.sin(idle * 0.7)
    group.position.y = Math.sin(idle * 0.8) * 0.1 * intact
    group.scale.setScalar(1 - breakAvg * 0.07)

    const coreLight = coreLightRef.current
    if (coreLight) {
      coreLight.intensity = breakAvg * 3.2
      coreLight.position.set(0, 0.35 * breakAvg, 0)
    }
  })

  return (
    <group ref={groupRef}>
      <pointLight ref={coreLightRef} color="#f8a800" intensity={0} distance={9} decay={2} />
      {cubes.map((cube, i) => (
        <mesh
          key={i}
          ref={(mesh) => {
            meshRefs.current[i] = mesh
          }}
          position={reduceMotion ? cube.target : cube.start}
          scale={reduceMotion ? cube.targetScale : 0.001}
        >
          <boxGeometry args={[CELL - GAP, CELL - GAP, CELL - GAP]} />
          <meshLambertMaterial color={COLORS[cube.cell]} />
        </mesh>
      ))}
    </group>
  )
}

function FallbackLogo() {
  return (
    <div className="relative mx-auto flex h-36 w-full min-w-0 max-w-full items-center justify-center sm:h-40 sm:max-w-lg lg:h-56">
      <Image src="/cellar-logo.png" alt="" width={128} height={128} />
    </div>
  )
}

class CanvasErrorBoundary extends Component<
  { children: ReactNode; fallback: ReactNode },
  { failed: boolean }
> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    if (this.state.failed) return this.props.fallback
    return this.props.children
  }
}

export function AnimatedLogo() {
  const hoverRef = useRef(false)
  const entryRef = useRef(new THREE.Vector2(0, 1))
  const [reduceMotion, setReduceMotion] = useState(false)
  const [mounted, setMounted] = useState(false)

  useEffect(() => {
    setMounted(true)
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    setReduceMotion(mq.matches)
    const onChange = () => setReduceMotion(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  if (!mounted) {
    return (
      <div className="relative mx-auto h-[min(32vh,16rem)] w-full min-w-0 max-w-full sm:h-[min(42vh,22rem)] sm:max-w-lg lg:h-[min(52vh,28rem)] lg:max-w-none" />
    )
  }

  return (
    <CanvasErrorBoundary fallback={<FallbackLogo />}>
      <div
        className="relative mx-auto h-[min(32vh,16rem)] w-full min-w-0 max-w-full cursor-pointer sm:h-[min(42vh,22rem)] sm:max-w-lg lg:h-[min(52vh,28rem)] lg:max-w-none"
        aria-hidden="true"
        onPointerEnter={(event) => {
          const rect = event.currentTarget.getBoundingClientRect()
          const dx = rect.left + rect.width / 2 - event.clientX
          const dy = rect.top + rect.height / 2 - event.clientY
          const len = Math.hypot(dx, dy)
          // Screen-space direction of travel: from the entry point toward the
          // center (y flipped so it matches world-up).
          if (len > 1) entryRef.current.set(dx / len, -dy / len)
          else entryRef.current.set(0, 1)
          hoverRef.current = true
        }}
        onPointerLeave={() => {
          hoverRef.current = false
        }}
      >
        <Canvas
          orthographic
          dpr={[1, 2]}
          flat
          gl={{
            antialias: true,
            alpha: true,
            premultipliedAlpha: true,
          }}
          camera={{ position: [7, 6.2, 9], near: 0.1, far: 100 }}
          style={{ position: 'absolute', inset: 0 }}
          onCreated={({ gl, camera }) => {
            gl.setClearColor(0x000000, 0)
            camera.lookAt(0, 0, 0)
          }}
        >
          <FitOrthoCamera />
          <ambientLight intensity={0.62} />
          <directionalLight intensity={1.15} position={[-5, 9, 4]} />
          <directionalLight color="#9eb4cc" intensity={0.38} position={[7, 1.5, 3]} />
          <directionalLight color="#ffe0a8" intensity={0.18} position={[2, 4, -6]} />
          <LogoScene hovered={hoverRef} entry={entryRef} reduceMotion={reduceMotion} />
        </Canvas>
      </div>
    </CanvasErrorBoundary>
  )
}
