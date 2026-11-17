<script lang="ts">
  import { lattice, unit, type Vec3 } from '../globe/lattice';
  import type { Place } from '../globe/places';
  import type { RailLook, Tone } from '../view';

  // The picture of Home: the Earth as dots, and the beam from this computer
  // to the country of the server. Drawn on a plain 2D canvas: a few thousand
  // dots need two lines of projection, not a 3D library. Everything the
  // globe says is also said in words next to it, so it is hidden from
  // screen readers.
  let {
    tone,
    beam,
    from,
    to,
    rate,
    active,
  }: {
    tone: Tone;
    /** How the path through the VPN looks: the beam is that path. */
    beam: RailLook;
    from: Place;
    /** Null when the country of the server is not known: no beam then. */
    to: Place | null;
    /** Bytes per second through the VPN; null when not known yet. */
    rate: number | null;
    /** False while nobody looks at the window: the globe stands still. */
    active: boolean;
  } = $props();

  type Rgb = [number, number, number];

  const dots = lattice();
  const STEPS = 64;
  const TAU = Math.PI * 2;

  let canvas: HTMLCanvasElement | undefined = $state();
  let box = $state({ w: 0, h: 0 });
  // Bumped when the theme changes: the colours are read from CSS again.
  let themeRev = $state(0);
  const motion = matchMedia('(prefers-reduced-motion: reduce)');
  let calm = $state(motion.matches);

  $effect(() => {
    const update = () => (calm = motion.matches);
    motion.addEventListener('change', update);
    const theme = new MutationObserver(() => themeRev++);
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    return () => {
      motion.removeEventListener('change', update);
      theme.disconnect();
    };
  });

  $effect(() => {
    if (!canvas) return;
    const el = canvas;
    const sizes = new ResizeObserver(() => (box = { w: el.clientWidth, h: el.clientHeight }));
    sizes.observe(el);
    return () => sizes.disconnect();
  });

  function hex(value: string, fallback: Rgb): Rgb {
    const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(value.trim());
    return m ? [parseInt(m[1]!, 16), parseInt(m[2]!, 16), parseInt(m[3]!, 16)] : fallback;
  }

  interface Palette {
    a: Rgb;
    b: Rgb;
    spark: Rgb;
    /** The colour of the card behind the globe. */
    bed: Rgb;
    sea: number;
  }

  let known: { key: string; colors: Palette } | null = null;

  /** The colours of a state, from the tokens of the current theme. */
  function palette(el: HTMLElement, state: Tone): Palette {
    // "Connecting" and "something is wrong" are the same amber everywhere else too.
    const family = state === 'busy' ? 'warn' : state;
    // Asking CSS sixty times a second is wasteful: once per state and theme.
    const key = `${family}:${themeRev}`;
    if (known?.key === key) return known.colors;
    const css = getComputedStyle(el);
    const grey: Rgb = [128, 128, 128];
    const colors = {
      a: hex(css.getPropertyValue(`--globe-${family}-a`), grey),
      b: hex(css.getPropertyValue(`--globe-${family}-b`), grey),
      spark: hex(css.getPropertyValue('--globe-spark'), [255, 255, 255]),
      bed: hex(css.getPropertyValue('--surface'), [0, 0, 0]),
      sea: Number(css.getPropertyValue('--globe-sea')) || 0.2,
    };
    known = { key, colors };
    return colors;
  }

  const rgba = (c: Rgb, alpha: number) => `rgb(${c[0] | 0} ${c[1] | 0} ${c[2] | 0} / ${alpha})`;

  // 0 for silence, 1 for ten megabytes a second and more; logarithmic,
  // because a video call and a download differ by orders of magnitude.
  function load(bytes: number | null): number {
    if (bytes === null) return 0.35;
    return Math.min(Math.log10(1 + bytes / 20_000) / 2.7, 1);
  }

  // What changes smoothly from frame to frame. Plain variables: the canvas
  // is redrawn by the loop, not by reactivity.
  let last = 0;
  let clock = 0;
  let lon = 0;
  let lat = 0;
  let spin = 0;
  let placed = false;
  let grow = 0;
  let drawnBeam: RailLook | null = null;
  let drawnTo = '';
  let ca: Rgb | null = null;
  let cb: Rgb | null = null;

  function ease(from: number, to: number, k: number): number {
    return from + (to - from) * k;
  }

  /** The shortest way round from one longitude to another. */
  function turn(from: number, to: number): number {
    let d = (to - from) % TAU;
    if (d > Math.PI) d -= TAU;
    if (d < -Math.PI) d += TAU;
    return d;
  }

  let layer: HTMLCanvasElement | null = null;
  let layerAt = -Infinity;
  let layerLon = 0;
  let layerLat = 0;

  /** The ball: its body, the haze round it and the dots, washed with the state's gradient. */
  function paintDots(
    g: CanvasRenderingContext2D,
    w: number,
    h: number,
    R: number,
    dpr: number,
    cl: number,
    sl: number,
    cp: number,
    sp: number,
    seaAlpha: number,
    ca: Rgb,
    cb: Rgb,
  ): void {
    const cx = w / 2;
    const cy = h / 2;
    g.setTransform(1, 0, 0, 1, 0, 0);
    g.globalCompositeOperation = 'source-over';
    g.globalAlpha = 1;
    g.clearRect(0, 0, w, h);

    // The body of the sphere: a faint light from the upper left.
    const body = g.createRadialGradient(cx - R * 0.35, cy - R * 0.4, R * 0.1, cx, cy, R);
    body.addColorStop(0, 'rgb(255 255 255 / 0.13)');
    body.addColorStop(1, 'rgb(255 255 255 / 0.03)');
    g.fillStyle = body;
    g.beginPath();
    g.arc(cx, cy, R, 0, TAU);
    g.fill();

    // And a thin haze around it, so the ball does not end in a hard edge.
    const air = g.createRadialGradient(cx, cy, R, cx, cy, R * 1.16);
    air.addColorStop(0, 'rgb(255 255 255 / 0.2)');
    air.addColorStop(0.35, 'rgb(255 255 255 / 0.07)');
    air.addColorStop(1, 'rgb(255 255 255 / 0)');
    g.fillStyle = air;
    g.beginPath();
    g.arc(cx, cy, R * 1.16, 0, TAU);
    g.arc(cx, cy, R, 0, TAU, true);
    g.fill();

    // Dots in four shades by depth: the far edge fades, as on a real ball.
    const space = (R * Math.PI) / 100;
    const landR = space * 0.3;
    const seaR = Math.max(space * 0.14, 0.5 * dpr);
    const land = [new Path2D(), new Path2D(), new Path2D(), new Path2D()];
    const sea = [new Path2D(), new Path2D(), new Path2D(), new Path2D()];
    const wave = beam === 'waiting' ? 0.4 : 0.16;
    const xyz = dots.xyz;
    for (let i = 0, n = dots.count; i < n; i++) {
      const x = xyz[i * 3]!;
      const y = xyz[i * 3 + 1]!;
      const z = xyz[i * 3 + 2]!;
      const z1 = z * cl + x * sl;
      const z2 = y * sp + z1 * cp;
      if (z2 <= 0.02) continue;
      const x1 = x * cl - z * sl;
      const y2 = y * cp - z1 * sp;
      const sx = cx + R * x1;
      const sy = cy - R * y2;
      const shade = Math.min(3, (z2 * 4) | 0);
      if (dots.land[i]) {
        // A slow swell runs over the continents; faster while connecting.
        const swell = 1 + wave * Math.sin(5 * (x1 * 0.6 + y2 * 0.8) - clock * 1.4);
        const r = landR * (0.5 + 0.5 * z2) * swell;
        const path = land[shade]!;
        path.moveTo(sx + r, sy);
        path.arc(sx, sy, r, 0, TAU);
      } else {
        const r = seaR * (0.6 + 0.4 * z2);
        sea[shade]!.rect(sx - r, sy - r, r * 2, r * 2);
      }
    }
    g.fillStyle = '#fff';
    for (let s = 0; s < 4; s++) {
      g.globalAlpha = seaAlpha * (0.4 + 0.2 * s);
      g.fill(sea[s]!);
      g.globalAlpha = 0.4 + 0.2 * s;
      g.fill(land[s]!);
    }

    // Everything drawn so far takes the gradient of the state.
    g.globalAlpha = 1;
    g.globalCompositeOperation = 'source-in';
    const wash = g.createLinearGradient(cx - R * 0.7, cy - R, cx + R * 0.7, cy + R);
    wash.addColorStop(0, rgba(ca, 1));
    wash.addColorStop(1, rgba(cb, 1));
    g.fillStyle = wash;
    g.fillRect(0, 0, w, h);
    g.globalCompositeOperation = 'source-over';
  }

  function draw(now: number, snap: boolean): void {
    const el = canvas;
    const ctx = el?.getContext('2d');
    if (!el || !ctx || box.w === 0 || box.h === 0) return;

    const dpr = window.devicePixelRatio || 1;
    const w = Math.round(box.w * dpr);
    const h = Math.round(box.h * dpr);
    if (el.width !== w || el.height !== h) {
      el.width = w;
      el.height = h;
    }

    const dt = snap || last === 0 ? 0 : Math.min((now - last) / 1000, 0.1);
    last = now;
    clock += dt;

    // Colours drift to those of the new state instead of jumping.
    const want = palette(el, tone);
    const kc = snap || !ca ? 1 : 1 - Math.exp(-dt / 0.35);
    ca = ca ? [ease(ca[0], want.a[0], kc), ease(ca[1], want.a[1], kc), ease(ca[2], want.a[2], kc)] : want.a;
    cb = cb ? [ease(cb[0], want.b[0], kc), ease(cb[1], want.b[1], kc), ease(cb[2], want.b[2], kc)] : want.b;

    const a = unit(from[0], from[1]);
    const b = to ? unit(to[0], to[1]) : null;
    const aimed = b !== null && beam !== 'idle';

    // Where the globe is turned. With a beam: to the middle of its way, a
    // little low, so that the beam is above the button. Without one: slowly
    // round, past this computer.
    let wantLon: number;
    let wantLat: number;
    if (aimed) {
      const mx = a[0] + b[0];
      const my = a[1] + b[1];
      const mz = a[2] + b[2];
      const len = Math.hypot(mx, my, mz) || 1;
      wantLon = Math.atan2(mx / len, mz / len) + (snap ? 0 : 0.12 * Math.sin(clock * 0.25));
      wantLat = Math.asin(my / len) - 0.16 + (snap ? 0 : 0.04 * Math.sin(clock * 0.19));
    } else {
      spin += dt * (TAU / 110);
      wantLon = (from[1] * Math.PI) / 180 + spin;
      wantLat = ((from[0] * Math.PI) / 180) * 0.45;
    }
    wantLat = Math.max(-0.9, Math.min(0.9, wantLat));
    const kv = snap || !placed ? 1 : 1 - Math.exp(-dt / 0.6);
    lon += turn(lon, wantLon) * kv;
    lat += (wantLat - lat) * kv;
    placed = true;
    // The turn already made stays in the angle; only the future one restarts.
    if (aimed) spin = turn((from[1] * Math.PI) / 180, lon);

    const cx = w / 2;
    const cy = h / 2;
    const R = Math.min(w, h) * 0.5 * 0.8;

    // The dots are nearly all the cost of a frame, and the ball turns by
    // less than a pixel in a sixtieth of a second. So they go into a layer
    // of their own, painted again fifteen times a second, or at once when
    // the ball has turned by more than a pixel or the colours are on their
    // way to a new state. The beam is painted over it as often as it moves.
    layer ??= document.createElement('canvas');
    const resized = layer.width !== w || layer.height !== h;
    const moved = R * (Math.abs(turn(layerLon, lon)) + Math.abs(lat - layerLat));
    const fading = Math.abs(ca[0] - want.a[0]) + Math.abs(ca[1] - want.a[1]) + Math.abs(ca[2] - want.a[2]) > 1.5;
    const fresh = snap || resized || fading || moved > 1.2 * dpr || now - layerAt >= 66;
    if (fresh) {
      if (resized) {
        layer.width = w;
        layer.height = h;
      }
      const g = layer.getContext('2d');
      if (!g) return;
      paintDots(g, w, h, R, dpr, Math.cos(lon), Math.sin(lon), Math.cos(lat), Math.sin(lat), want.sea, ca, cb);
      layerLon = lon;
      layerLat = lat;
      layerAt = now;
    }
    // A beam that stands still needs no frames between those of the dots.
    const moving = b !== null && (beam === 'flow' || beam === 'waiting');
    if (!fresh && !moving) return;

    // The beam follows the ball as painted, not as it is about to be.
    const cl = Math.cos(layerLon);
    const sl = Math.sin(layerLon);
    const cp = Math.cos(layerLat);
    const sp = Math.sin(layerLat);

    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.globalCompositeOperation = 'source-over';
    ctx.globalAlpha = 1;
    ctx.clearRect(0, 0, w, h);
    ctx.drawImage(layer, 0, 0);

    const project = (p: Vec3, lift: number): [number, number, boolean] => {
      const z1 = p[2] * cl + p[0] * sl;
      const x1 = (p[0] * cl - p[2] * sl) * lift;
      const y2 = (p[1] * cp - z1 * sp) * lift;
      const z2 = (p[1] * sp + z1 * cp) * lift;
      // In front of the ball, or behind it but sticking out past its edge.
      return [cx + R * x1, cy - R * y2, z2 > 0 || x1 * x1 + y2 * y2 > 1.02];
    };

    const cross = (x: number, y: number, color: string) => {
      const d = 4.5 * dpr;
      ctx.strokeStyle = color;
      ctx.lineWidth = 2 * dpr;
      ctx.setLineDash([]);
      ctx.beginPath();
      ctx.moveTo(x - d, y - d);
      ctx.lineTo(x + d, y + d);
      ctx.moveTo(x + d, y - d);
      ctx.lineTo(x - d, y + d);
      ctx.stroke();
    };

    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';

    // The beam "shoots" to the server when the VPN comes up or the server
    // changes; from "connecting" to "connected" it is already there.
    const key = to ? `${to[0]},${to[1]}` : '';
    if (beam !== drawnBeam || key !== drawnTo) {
      const standing = key === drawnTo && (drawnBeam === 'waiting' || drawnBeam === 'down' || drawnBeam === 'flow');
      if (beam === 'flow' && !standing) grow = 0;
      drawnBeam = beam;
      drawnTo = key;
    }
    grow = snap ? 1 : Math.min(1, grow + dt / 0.8);

    const [ax, ay, aSeen] = project(a, 1);

    if (b && beam !== 'idle') {
      const omega = Math.acos(Math.max(-1, Math.min(1, a[0] * b[0] + a[1] * b[1] + a[2] * b[2])));
      const height = Math.max(0.12, Math.min(0.42, 0.1 + omega * 0.22));
      const so = Math.sin(omega) || 1;
      const pts: [number, number, boolean][] = [];
      for (let i = 0; i <= STEPS; i++) {
        const t = i / STEPS;
        const ka = omega < 1e-3 ? 1 - t : Math.sin((1 - t) * omega) / so;
        const kb = omega < 1e-3 ? t : Math.sin(t * omega) / so;
        pts.push(project([a[0] * ka + b[0] * kb, a[1] * ka + b[1] * kb, a[2] * ka + b[2] * kb], 1 + height * Math.sin(Math.PI * t)));
      }
      const at = (t: number): [number, number, boolean] => {
        const f = Math.max(0, Math.min(1, t)) * STEPS;
        const i = Math.min(STEPS - 1, f | 0);
        const p = pts[i]!;
        const q = pts[i + 1]!;
        return [p[0] + (q[0] - p[0]) * (f - i), p[1] + (q[1] - p[1]) * (f - i), p[2] && q[2]];
      };

      // How far the beam reaches: all the way, creeping while connecting,
      // cut short at the start while the lock holds the internet closed.
      let upto = 1;
      if (beam === 'flow') upto = 1 - (1 - grow) ** 3;
      else if (beam === 'waiting') upto = snap ? 1 : Math.min(1, (clock % 1.9) / 1.3);
      else if (beam === 'closed') upto = 0.22;

      const [bx, by, bSeen] = at(1);
      const trail = new Path2D();
      let pen = false;
      const end = upto * STEPS;
      for (let i = 0; i <= STEPS && i <= end; i++) {
        const p = pts[i]!;
        if (!p[2]) {
          pen = false;
          continue;
        }
        if (pen) trail.lineTo(p[0], p[1]);
        else trail.moveTo(p[0], p[1]);
        pen = true;
      }
      const tip = at(upto);
      if (pen && tip[2]) trail.lineTo(tip[0], tip[1]);

      // A dark bed under the beam: over the continents the beam is the same
      // colour as the dots, and a dashed one would vanish without it.
      const thick = (beam === 'flow' ? 1.3 + 1.2 * load(rate) : 1.6) * dpr;
      ctx.setLineDash([]);
      ctx.strokeStyle = rgba(want.bed, 0.7);
      ctx.lineWidth = thick + 3 * dpr;
      ctx.stroke(trail);

      const line = ctx.createLinearGradient(ax, ay, bx, by);
      line.addColorStop(0, rgba(ca, 0.95));
      line.addColorStop(1, rgba(cb, 0.95));
      ctx.strokeStyle = line;
      ctx.lineWidth = thick;
      ctx.setLineDash(beam === 'flow' ? [] : [4 * dpr, 4 * dpr]);
      ctx.globalAlpha = beam === 'flow' ? 0.85 : 1;
      ctx.stroke(trail);
      ctx.setLineDash([]);
      ctx.globalAlpha = 1;

      // Sparks run along a working beam, the faster the more traffic.
      if (beam === 'flow' && grow >= 1 && !snap) {
        const period = 2.6 - 1.5 * load(rate);
        const tail = 0.3;
        for (let k = 0; k < 2; k++) {
          const head = ((clock / period + k * 0.5) % 1) * (1 + tail);
          for (let j = 0; j < 14; j++) {
            const t0 = head - tail * (1 - j / 14);
            const t1 = head - tail * (1 - (j + 1) / 14);
            if (t1 < 0 || t0 > 1) continue;
            const p = at(t0);
            const q = at(t1);
            if (!p[2] || !q[2]) continue;
            ctx.strokeStyle = rgba(want.spark, ((j + 1) / 14) ** 2 * 0.9);
            ctx.lineWidth = (1.2 + 1.4 * (j / 14)) * dpr;
            ctx.beginPath();
            ctx.moveTo(p[0], p[1]);
            ctx.lineTo(q[0], q[1]);
            ctx.stroke();
          }
        }
      }

      if (beam === 'closed') {
        if (tip[2]) cross(tip[0], tip[1], rgba(ca, 1));
      } else if (bSeen) {
        if (beam === 'down') {
          cross(bx, by, rgba(ca, 1));
        } else if (upto >= 1) {
          // The server: a point with rings going out of it.
          if (!snap) {
            for (let k = 0; k < 2; k++) {
              const u = (clock / 2.2 + k * 0.5) % 1;
              ctx.strokeStyle = rgba(cb, (1 - u) * 0.7);
              ctx.lineWidth = 1.2 * dpr;
              ctx.beginPath();
              ctx.arc(bx, by, (4 + 12 * u) * dpr, 0, TAU);
              ctx.stroke();
            }
          }
          ctx.fillStyle = rgba(cb, 1);
          ctx.beginPath();
          ctx.arc(bx, by, 3.4 * dpr, 0, TAU);
          ctx.fill();
          ctx.fillStyle = rgba(want.spark, 0.9);
          ctx.beginPath();
          ctx.arc(bx, by, 1.4 * dpr, 0, TAU);
          ctx.fill();
        }
      }
    }

    // This computer: always marked, with or without a beam.
    if (aSeen) {
      ctx.strokeStyle = rgba(ca, 0.55);
      ctx.lineWidth = 1.2 * dpr;
      ctx.beginPath();
      ctx.arc(ax, ay, 6.5 * dpr, 0, TAU);
      ctx.stroke();
      ctx.fillStyle = rgba(ca, 1);
      ctx.beginPath();
      ctx.arc(ax, ay, 3 * dpr, 0, TAU);
      ctx.fill();
    }
  }

  // The loop runs only while the window is looked at; otherwise, and for
  // those who asked for less motion, one still frame per change. At most
  // thirty frames a second: every frame the webview shows costs the GPU
  // process the same whatever is in it, and the sparks on the beam look
  // the same at thirty as at sixty (measured in the real window).
  $effect(() => {
    if (!canvas || !active || calm) return;
    let drawn = 0;
    let frame = requestAnimationFrame(function tick(now) {
      if (now - drawn >= 31) {
        drawn = now;
        draw(now, false);
      }
      frame = requestAnimationFrame(tick);
    });
    return () => {
      cancelAnimationFrame(frame);
      last = 0;
    };
  });

  $effect(() => {
    void [tone, beam, from, to, rate, box.w, box.h, themeRev];
    if (canvas && calm) draw(performance.now(), true);
  });
</script>

<canvas bind:this={canvas} aria-hidden="true"></canvas>

<style>
  canvas {
    display: block;
    width: 100%;
    height: 100%;
  }
</style>
