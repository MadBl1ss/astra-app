<script lang="ts">
  // The night behind the globe of Home: a few hundred faint stars, some of
  // which slowly breathe. Pure decoration, so hidden from screen readers and
  // drawn as cheaply as possible: the still stars are painted once into a
  // layer of their own, and only the breathing ones are redrawn, a dozen
  // times a second, and only while the window is looked at.
  let { active }: { /** False while nobody looks at the window: the sky stands still. */ active: boolean } = $props();

  type Rgb = [number, number, number];

  interface Star {
    /** Place in the box, 0…1 on both axes: a resized window keeps its sky. */
    x: number;
    y: number;
    /** Radius in CSS pixels. */
    r: number;
    /** Brightness at rest, 0…1, before --star-light. */
    a: number;
    /** 0 for a star that holds still, otherwise how deep it breathes. */
    beat: number;
    /** Seconds per breath, and where in it the star starts. */
    period: number;
    phase: number;
    warm: boolean;
  }

  // Fixed seed: the same sky every time the screen opens, not a new one
  // each visit, which would read as noise rather than as a place.
  function random(seed: number): () => number {
    return () => {
      seed = (seed + 0x6d2b79f5) | 0;
      let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  // Enough for the largest screen. Every star has its own random place, so
  // the first n of them are as evenly spread as all of them: a smaller box
  // takes fewer, and the density stays the same.
  const FIELD = 1200;
  const stars: Star[] = (() => {
    const next = random(0x5eed);
    const out: Star[] = [];
    for (let i = 0; i < FIELD; i++) {
      const roll = next();
      // Most stars are barely there; a few are noticeably brighter.
      const r = roll < 0.78 ? 0.5 + next() * 0.35 : roll < 0.96 ? 0.85 + next() * 0.3 : 1.15 + next() * 0.4;
      out.push({
        x: next(),
        y: next(),
        r,
        a: roll < 0.78 ? 0.3 + next() * 0.35 : 0.6 + next() * 0.4,
        beat: next() < 0.3 ? 0.35 + next() * 0.5 : 0,
        period: 3.5 + next() * 6,
        phase: next() * Math.PI * 2,
        warm: next() < 0.18,
      });
    }
    return out;
  })();

  let canvas: HTMLCanvasElement | undefined = $state();
  let box = $state({ w: 0, h: 0 });
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

  const rgba = (c: Rgb, alpha: number) => `rgb(${c[0]} ${c[1]} ${c[2]} / ${alpha})`;

  /** What the current theme asks of the sky; light 0 means no sky at all. */
  const look = $derived.by(() => {
    void themeRev;
    if (!canvas) return null;
    const css = getComputedStyle(canvas);
    return {
      cold: hex(css.getPropertyValue('--star-a'), [255, 255, 255]),
      warm: hex(css.getPropertyValue('--star-b'), [255, 255, 255]),
      light: Math.max(0, Math.min(1, Number(css.getPropertyValue('--star-light')) || 0)),
    };
  });

  /** The stars of a box of this size: sparse on purpose, one per 4500 square pixels. */
  function visible(w: number, h: number): Star[] {
    return stars.slice(0, Math.min(FIELD, Math.round((w * h) / 4500)));
  }

  let layer: HTMLCanvasElement | null = null;
  let breathing: Star[] = [];

  /** Paints the still stars into the layer; the breathing ones are kept apart. */
  function prepare(): boolean {
    const el = canvas;
    const sky = look;
    if (!el || !sky || sky.light === 0 || box.w === 0 || box.h === 0) return false;
    const dpr = window.devicePixelRatio || 1;
    const w = Math.round(box.w * dpr);
    const h = Math.round(box.h * dpr);
    el.width = w;
    el.height = h;
    layer ??= document.createElement('canvas');
    layer.width = w;
    layer.height = h;
    const g = layer.getContext('2d');
    if (!g) return false;
    g.clearRect(0, 0, w, h);
    breathing = [];
    for (const s of visible(box.w, box.h)) {
      if (s.beat > 0 && !calm) {
        breathing.push(s);
        continue;
      }
      star(g, s, w, h, dpr, s.a * sky.light, sky);
    }
    return true;
  }

  function star(
    g: CanvasRenderingContext2D,
    s: Star,
    w: number,
    h: number,
    dpr: number,
    alpha: number,
    sky: { cold: Rgb; warm: Rgb },
  ): void {
    const x = s.x * w;
    const y = s.y * h;
    const r = s.r * dpr;
    const c = s.warm ? sky.warm : sky.cold;
    // The brightest few get a soft halo; for the rest a dot is all the eye sees.
    if (s.r > 1) {
      const halo = g.createRadialGradient(x, y, 0, x, y, r * 4);
      halo.addColorStop(0, rgba(c, alpha * 0.35));
      halo.addColorStop(1, rgba(c, 0));
      g.fillStyle = halo;
      g.beginPath();
      g.arc(x, y, r * 4, 0, Math.PI * 2);
      g.fill();
    }
    g.fillStyle = rgba(c, alpha);
    g.beginPath();
    g.arc(x, y, r, 0, Math.PI * 2);
    g.fill();
  }

  function draw(now: number): void {
    const el = canvas;
    const sky = look;
    const ctx = el?.getContext('2d');
    if (!el || !ctx || !layer || !sky) return;
    const dpr = window.devicePixelRatio || 1;
    ctx.clearRect(0, 0, el.width, el.height);
    ctx.drawImage(layer, 0, 0);
    const t = now / 1000;
    for (const s of breathing) {
      // A breath is mostly the star at rest with a slow dip and rise; a plain
      // sine would keep every star visibly moving all the time.
      const wave = 0.5 + 0.5 * Math.sin((t / s.period) * Math.PI * 2 + s.phase);
      const k = 1 - s.beat * wave * wave;
      star(ctx, s, el.width, el.height, dpr, s.a * k * sky.light, sky);
    }
  }

  // A still sky, painted again whenever its size, theme or motion setting
  // changes; if the window is looked at, the breathing loop takes it from there.
  $effect(() => {
    void [box.w, box.h, look, calm];
    if (prepare()) draw(performance.now());
    else if (canvas) canvas.getContext('2d')?.clearRect(0, 0, canvas.width, canvas.height);
  });

  // Twelve frames a second: a breath takes seconds, faster frames would
  // show nothing more and only keep the GPU busy.
  $effect(() => {
    void [box.w, box.h, look];
    if (!canvas || !active || calm || !look || look.light === 0 || breathing.length === 0) return;
    let drawn = 0;
    let frame = requestAnimationFrame(function tick(now) {
      if (now - drawn >= 83) {
        drawn = now;
        draw(now);
      }
      frame = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(frame);
  });
</script>

<canvas bind:this={canvas} aria-hidden="true"></canvas>

<style>
  canvas {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    pointer-events: none;
  }
</style>
