'use client'

import Image from 'next/image'
import { useEffect, useRef, useState } from 'react'
import * as THREE from 'three'

const P = 'P'
const S = 'S'
const E = '.'

type Cell = typeof P | typeof S | typeof E

const GRID: Cell[][][] = [
  [
    [P, P, P],
    [P, P, P],
    [P, P, P],
  ],
  [
    [P, P, P],
    [P, S, E],
    [P, E, E],
  ],
  [
    [P, P, P],
    [P, E, E],
    [P, E, E],
  ],
]

const COLORS = {
  P: 0xd9dde3,
  S: 0xf5a013,
} as const

function makeEnvironment() {
  const env = new THREE.Scene()
  env.background = new THREE.Color(0x9aa3af)
  const panel = (
    w: number,
    h: number,
    col: number,
    x: number,
    y: number,
    z: number,
    ry = 0,
    rx = 0,
  ) => {
    const m = new THREE.Mesh(
      new THREE.PlaneGeometry(w, h),
      new THREE.MeshBasicMaterial({ color: col, side: THREE.DoubleSide }),
    )
    m.position.set(x, y, z)
    m.rotation.set(rx, ry, 0)
    env.add(m)
  }
  panel(14, 4, 0xffffff, 0, 6, 0, 0, Math.PI / 2)
  panel(6, 10, 0xe9edf2, -9, 0, 0, Math.PI / 2)
  panel(6, 10, 0x3b5678, 9, 0, 0, Math.PI / 2)
  panel(10, 6, 0xffe1b0, 0, 1, -9)
  panel(14, 14, 0x22293a, 0, -6, 0, 0, Math.PI / 2)
  return env
}

function roundedBox(size: number, radius: number, segments = 6) {
  const g = new THREE.BoxGeometry(size, size, size, segments, segments, segments)
  const pos = g.attributes.position
  const nor = g.attributes.normal
  const inner = size / 2 - radius
  const v = new THREE.Vector3()
  const c = new THREE.Vector3()
  for (let i = 0; i < pos.count; i++) {
    v.fromBufferAttribute(pos, i)
    c.set(
      THREE.MathUtils.clamp(v.x, -inner, inner),
      THREE.MathUtils.clamp(v.y, -inner, inner),
      THREE.MathUtils.clamp(v.z, -inner, inner),
    )
    v.sub(c).normalize()
    nor.setXYZ(i, v.x, v.y, v.z)
    v.multiplyScalar(radius).add(c)
    pos.setXYZ(i, v.x, v.y, v.z)
  }
  return g
}

function addSpot(
  scene: THREE.Scene,
  color: number,
  intensity: number,
  x: number,
  y: number,
  z: number,
  shadow = false,
) {
  const l = new THREE.SpotLight(color, intensity, 0, Math.PI / 6, 0.45, 1.6)
  l.position.set(x, y, z)
  l.target.position.set(0, -0.3, 0)
  if (shadow) {
    l.castShadow = true
    l.shadow.mapSize.set(1024, 1024)
    l.shadow.bias = -0.0008
  }
  scene.add(l, l.target)
  return l
}

const ditherVertex = /* glsl */ `
  varying vec2 vUv;
  void main(){
    vUv = uv;
    gl_Position = vec4(position.xy, 0.0, 1.0);
  }
`

const ditherFragment = /* glsl */ `
  precision highp float;
  uniform sampler2D tDiffuse;
  uniform vec2  resolution;
  uniform float pixelSize, levels, strength;
  varying vec2 vUv;

  float bayer8(vec2 p){
    int x = int(mod(p.x, 8.0)), y = int(mod(p.y, 8.0));
    int m[64];
    m[ 0]= 0; m[ 1]=32; m[ 2]= 8; m[ 3]=40; m[ 4]= 2; m[ 5]=34; m[ 6]=10; m[ 7]=42;
    m[ 8]=48; m[ 9]=16; m[10]=56; m[11]=24; m[12]=50; m[13]=18; m[14]=58; m[15]=26;
    m[16]=12; m[17]=44; m[18]= 4; m[19]=36; m[20]=14; m[21]=46; m[22]= 6; m[23]=38;
    m[24]=60; m[25]=28; m[26]=52; m[27]=20; m[28]=62; m[29]=30; m[30]=54; m[31]=22;
    m[32]= 3; m[33]=35; m[34]=11; m[35]=43; m[36]= 1; m[37]=33; m[38]= 9; m[39]=41;
    m[40]=51; m[41]=19; m[42]=59; m[43]=27; m[44]=49; m[45]=17; m[46]=57; m[47]=25;
    m[48]=15; m[49]=47; m[50]= 7; m[51]=39; m[52]=13; m[53]=45; m[54]= 5; m[55]=37;
    m[56]=63; m[57]=31; m[58]=55; m[59]=23; m[60]=61; m[61]=29; m[62]=53; m[63]=21;
    int i = y * 8 + x;
    for (int k = 0; k < 64; k++) { if (k == i) return (float(m[k]) + 0.5) / 64.0; }
    return 0.5;
  }

  void main(){
    vec2 px   = floor(vUv * resolution / pixelSize) * pixelSize;
    vec2 uv   = (px + pixelSize * 0.5) / resolution;
    vec4 src  = texture2D(tDiffuse, uv);
    if (src.a < 0.02) {
      gl_FragColor = vec4(0.0);
      return;
    }
    vec3 col  = src.rgb;
    float t   = (bayer8(px / pixelSize) - 0.5) * strength;
    vec3 q    = floor(col * levels + t + 0.5) / levels;
    q = clamp(q, 0.0, 1.0);
    gl_FragColor = vec4(q * src.a, src.a);
  }
`

function startScene(canvas: HTMLCanvasElement) {
  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: false,
    alpha: true,
    premultipliedAlpha: true,
  })
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
  renderer.setClearColor(0x000000, 0)
  renderer.shadowMap.enabled = true
  renderer.shadowMap.type = THREE.PCFSoftShadowMap
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  renderer.toneMappingExposure = 1.05

  const scene = new THREE.Scene()
  scene.background = null

  const camera = new THREE.PerspectiveCamera(28, 1, 0.1, 100)
  camera.position.set(7, 6.2, 9)
  camera.lookAt(0, 0, 0)

  const pmrem = new THREE.PMREMGenerator(renderer)
  const envRt = pmrem.fromScene(makeEnvironment(), 0.04)
  scene.environment = envRt.texture

  const CELL = 1.0
  const GAP = 0.03
  const RADIUS = 0.09
  const cubeGroup = new THREE.Group()
  scene.add(cubeGroup)

  const geo = roundedBox(CELL - GAP, RADIUS)
  const mats = {
    P: new THREE.MeshStandardMaterial({
      color: COLORS.P,
      metalness: 1.0,
      roughness: 0.18,
      envMapIntensity: 1.3,
    }),
    S: new THREE.MeshStandardMaterial({
      color: COLORS.S,
      metalness: 0.85,
      roughness: 0.28,
      envMapIntensity: 1.4,
    }),
  }

  GRID.forEach((layer, y) =>
    layer.forEach((row, z) =>
      row.forEach((cell, x) => {
        if (cell === E) return
        const m = new THREE.Mesh(geo, mats[cell])
        m.position.set((x - 1) * CELL, (y - 1) * CELL, (z - 1) * CELL)
        m.castShadow = m.receiveShadow = true
        cubeGroup.add(m)
      }),
    ),
  )

  const ground = new THREE.Mesh(
    new THREE.PlaneGeometry(40, 40),
    new THREE.ShadowMaterial({ opacity: 0.18 }),
  )
  ground.rotation.x = -Math.PI / 2
  ground.position.y = -2.3
  ground.receiveShadow = true
  scene.add(ground)

  scene.add(new THREE.AmbientLight(0xffffff, 0.25))
  const keyLight = addSpot(scene, 0xffffff, 140, 6, 9, 5, true)
  addSpot(scene, 0x9fc4ff, 120, -7, 6, -6)
  addSpot(scene, 0xffd9a3, 60, -5, 3, 7)
  addSpot(scene, 0xffffff, 70, 0, 11, 0)

  const rt = new THREE.WebGLRenderTarget(1, 1, { type: THREE.HalfFloatType })
  const ditherMat = new THREE.ShaderMaterial({
    uniforms: {
      tDiffuse: { value: rt.texture },
      resolution: { value: new THREE.Vector2() },
      pixelSize: { value: 2.0 },
      levels: { value: 7.0 },
      strength: { value: 1.0 },
    },
    vertexShader: ditherVertex,
    fragmentShader: ditherFragment,
    depthTest: false,
    depthWrite: false,
    transparent: true,
    premultipliedAlpha: true,
  })

  const postScene = new THREE.Scene()
  const postCamera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1)
  const postQuad = new THREE.Mesh(new THREE.PlaneGeometry(2, 2), ditherMat)
  postScene.add(postQuad)

  const resize = () => {
    const w = canvas.clientWidth
    const h = canvas.clientHeight
    if (w < 1 || h < 1) return
    renderer.setSize(w, h, false)
    camera.aspect = w / h
    camera.updateProjectionMatrix()
    const dpr = renderer.getPixelRatio()
    rt.setSize(Math.max(1, Math.floor(w * dpr)), Math.max(1, Math.floor(h * dpr)))
    ditherMat.uniforms.resolution.value.set(w * dpr, h * dpr)
  }

  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const clock = new THREE.Clock()
  let raf = 0
  let running = true

  const frame = () => {
    if (!running) return
    const t = clock.getElapsedTime()
    if (!reduceMotion) {
      cubeGroup.rotation.y = t * 0.45
      cubeGroup.rotation.x = Math.sin(t * 0.6) * 0.18
      cubeGroup.position.y = Math.sin(t * 0.9) * 0.08
      keyLight.position.x = 6 * Math.cos(t * 0.3)
      keyLight.position.z = 5 * Math.sin(t * 0.3) + 2
    } else {
      cubeGroup.rotation.set(0.35, 0.8, 0)
    }

    renderer.setRenderTarget(rt)
    renderer.setClearColor(0x000000, 0)
    renderer.render(scene, camera)
    renderer.setRenderTarget(null)
    renderer.setClearColor(0x000000, 0)
    renderer.render(postScene, postCamera)

    raf = requestAnimationFrame(frame)
  }

  const observer = new ResizeObserver(resize)
  observer.observe(canvas)
  resize()
  frame()

  return () => {
    running = false
    cancelAnimationFrame(raf)
    observer.disconnect()
    geo.dispose()
    mats.P.dispose()
    mats.S.dispose()
    ground.geometry.dispose()
    ;(ground.material as THREE.Material).dispose()
    postQuad.geometry.dispose()
    ditherMat.dispose()
    rt.dispose()
    envRt.dispose()
    pmrem.dispose()
    renderer.dispose()
  }
}

export function MetallicCube() {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    try {
      return startScene(canvas)
    } catch {
      setFailed(true)
    }
  }, [])

  if (failed) {
    return (
      <div className="relative mx-auto mb-8 flex h-32 w-full max-w-4xl items-center justify-center">
        <Image src="/cellar-logo.png" alt="" width={128} height={128} />
      </div>
    )
  }

  return (
    <div className="relative mx-auto h-[min(56vh,32rem)] w-full max-w-4xl sm:h-[min(62vh,38rem)]">
      <canvas
        ref={canvasRef}
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 block size-full"
      />
    </div>
  )
}
