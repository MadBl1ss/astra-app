<script lang="ts" module>
  export type Preset = 'current' | 'favorites' | 'best' | 'picked';
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import { flip } from 'svelte/animate';
  import Button from '../../components/Button.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import Icon from '../../components/Icon.svelte';
  import Segments from '../../components/Segments.svelte';
  import { bytes } from '../../format';
  import { ru } from '../../i18n/ru';
  import type { Server, SpeedResult, SpeedRun, SpeedSample, State } from '../../proto';
  import { bestOf, mbpsText, winner } from '../../rating';
  import { favoritesOf } from '../../servers';
  import { daemon } from '../../store.svelte';
  import { displayName, flagOf } from '../../view';
  import SpeedGauge from './SpeedGauge.svelte';

  // The speed test from start to end: what to measure and what it costs,
  // then the dial and the queue while it runs, then the results ranked, with
  // the fastest one a click away from being the server in use. Closing the
  // window does not stop the test; opening it again picks the run up.
  let {
    state: s,
    preset: first = 'current',
    picked = [],
    autostart = false,
    onclose,
  }: {
    state: State;
    preset?: Preset;
    /** The servers of the "picked" preset: the one whose row was clicked. */
    picked?: string[];
    /** Start at once: a row's button needs no second question. */
    autostart?: boolean;
    onclose: () => void;
  } = $props();

  const t = ru.speed;
  const MAX = 12;
  // A server's turn takes about this long: three pings and the download.
  const TURN_S = 11;

  let preset = $state<Preset>(untrack(() => first));
  const byId = $derived(new Map(s.servers.map((x) => [x.id, x])));
  const pools = $derived<Record<Preset, string[]>>({
    current: s.server.selected ? [s.server.selected] : [],
    favorites: favoritesOf(s)
      .slice(0, MAX)
      .map((x) => x.id),
    best: bestOf(s.servers, 5).map((x) => x.id),
    picked: picked.filter((id) => byId.has(id)),
  });
  const chosen = $derived(pools[preset]);
  const options = $derived(
    (['current', 'favorites', 'best', 'picked'] as const)
      .filter((p) => p !== 'picked' || picked.length > 0)
      .map((p) => ({ value: p, label: t.presets[p] })),
  );
  const emptyText = $derived(preset === 'favorites' ? t.noFavorites : preset === 'best' ? t.noBest : t.noCurrent);

  // ---- the run ----

  // The run as State has it while it goes; kept afterwards, so that its
  // results stay on screen once State has let go of it.
  let kept = $state<SpeedRun | null>(null);
  let stopped = $state(false);
  $effect(() => {
    const live = s.speed;
    untrack(() => {
      if (live) {
        if (kept && kept.started !== live.started) stopped = false;
        kept = live;
      }
    });
  });
  const run = $derived(s.speed ?? kept);
  const mode = $derived<'idle' | 'running' | 'done'>(s.speed ? 'running' : kept ? 'done' : 'idle');

  // The needle: a few samples a second, heard only while this is open.
  let sample = $state<SpeedSample | null>(null);
  let history = $state<number[]>([]);
  let pings = $state<(number | null)[]>([]);
  let onServer = $state<string | null>(null);
  $effect(() =>
    daemon.watchSpeed((x) => {
      if (x.id !== onServer) {
        onServer = x.id;
        pings = [];
        history = [];
        sample = null;
      }
      if (x.phase === 'ping') pings = [...pings, x.ping];
      else {
        sample = x;
        history = [...history.slice(-90), x.mbps];
      }
    }),
  );
  // A new server, or none: the needle goes back to rest.
  $effect(() => {
    const current = run?.current ?? null;
    untrack(() => {
      if (current !== onServer) {
        onServer = current;
        pings = [];
        history = [];
        sample = null;
      }
    });
  });

  const phase = $derived(mode === 'running' ? (s.speed!.stage === 'preparing' ? 'preparing' : (s.speed!.phase ?? 'between')) : null);
  const results = $derived(run?.results ?? []);
  const best = $derived(winner(results));
  const top = $derived(Math.max(1, ...results.map((r) => r.mbps ?? 0)));
  const needle = $derived(
    phase === 'download' && sample ? sample.mbps : mode === 'done' && best ? best.mbps! : 0,
  );

  /** The queue: what is measured, in order; once done, fastest first. */
  const rows = $derived.by(() => {
    if (!run) return [];
    const got = new Map(results.map((r) => [r.id, r]));
    const list = run.ids.map((id) => ({ id, server: byId.get(id) ?? null, result: got.get(id) ?? null }));
    if (mode !== 'done') return list;
    return [...list].sort((a, b) => (b.result?.mbps ?? -1) - (a.result?.mbps ?? -1));
  });

  // ---- the cost ----

  const perServer = $derived(run?.maxBytes ?? 25 * 1024 * 1024);
  const lowSubs = $derived.by(() => {
    const need = perServer * chosen.length;
    const subs = new Set(chosen.map((id) => byId.get(id)?.sub).filter((x): x is string => !!x));
    return s.subs.filter((sub) => {
      if (!subs.has(sub.id) || sub.totalBytes === null || sub.usedBytes === null) return false;
      return sub.totalBytes - sub.usedBytes < need * 20;
    });
  });

  // ---- actions ----

  let starting = $state(false);
  async function start(): Promise<void> {
    if (chosen.length === 0) return;
    starting = true;
    kept = null;
    stopped = false;
    await daemon.rpc('server.speedTest', { ids: chosen });
    starting = false;
  }

  function stop(): void {
    stopped = true;
    void daemon.rpc('server.testStop', undefined);
  }

  $effect(() => {
    if (untrack(() => autostart && mode === 'idle' && chosen.length > 0)) void start();
  });

  function nameOf(server: Server | null, id: string): string {
    return server ? displayName(server) : id;
  }

  function spark(values: number[]): string {
    if (values.length < 2) return '';
    const hi = Math.max(1, ...values);
    const w = 260;
    const h = 34;
    return values.map((v, i) => `${((i / (values.length - 1)) * w).toFixed(1)},${(h - (v / hi) * (h - 3)).toFixed(1)}`).join(' ');
  }
  const line = $derived(spark(history));
</script>

<Dialog title={t.title} width={760} {onclose}>
  <div class="body">
    <div class="dial">
      <SpeedGauge value={needle} live={phase === 'download'}>
        {#if phase === 'download' && sample}
          <span class="big">{mbpsText(sample.mbps)}</span>
          <span class="unit">{t.unit}</span>
        {:else if phase === 'ping' || (phase === 'between' && pings.length > 0)}
          {@const heard = pings.filter((x): x is number => x !== null)}
          <span class="big ping">{heard.length > 0 ? heard[heard.length - 1] : '…'}<small>{t.ms}</small></span>
          <span class="unit">{t.pingDots}</span>
        {:else if mode === 'done' && best}
          <span class="big">{mbpsText(best.mbps!)}</span>
          <span class="unit">{t.unit} · {t.winner}</span>
        {:else}
          <span class="big rest">0</span>
          <span class="unit">{t.unit}</span>
        {/if}
      </SpeedGauge>
      <div class="under">
        {#if mode === 'running'}
          {@const cur = run?.current ? byId.get(run.current) : null}
          <p class="now" aria-live="polite">
            {#if phase === 'preparing'}
              {t.preparing}
            {:else if cur}
              <span class="flag">{flagOf(cur) ?? ''}</span>
              <span class="nm">{displayName(cur)}</span>
              <span class="ph">{phase === 'ping' ? t.ping : t.download}</span>
            {:else}
              {t.between}
            {/if}
          </p>
          <div class="pings" aria-hidden="true">
            {#each { length: 3 } as _, i (i)}
              <span class="dot" class:got={pings[i] !== undefined && pings[i] !== null} class:miss={pings[i] === null}></span>
            {/each}
            {#if phase === 'download' && sample}
              <span class="prog">{t.progress(bytes(sample.bytes), (sample.elapsed / 1000).toFixed(1).replace('.', ','))}</span>
            {/if}
          </div>
          <svg class="spark" viewBox="0 0 260 34" preserveAspectRatio="none" aria-hidden="true">
            {#if line}
              <polygon class="area" points="0,34 {line} 260,34" />
              <polyline class="stroke" points={line} />
            {/if}
          </svg>
        {:else if mode === 'done'}
          <p class="now" role="status">
            {#if best}
              {@const w = byId.get(best.id) ?? null}
              {t.best(nameOf(w, best.id), mbpsText(best.mbps!))}
            {:else}
              {stopped ? t.stopped : t.none}
            {/if}
          </p>
          <p class="hint">{t.ceiling}</p>
        {:else}
          <p class="hint">{t.oneByOne}</p>
        {/if}
      </div>
    </div>

    <div class="side">
      {#if mode === 'idle'}
        <span class="cap">{t.what}</span>
        <Segments label={t.what} {options} value={preset} onchange={(v) => (preset = v)} />
        {#if chosen.length === 0}
          <p class="empty">{emptyText}</p>
        {:else}
          <ul class="pick">
            {#each chosen as id, i (id)}
              {@const x = byId.get(id) ?? null}
              <li style:--i={i}>
                <span class="flag">{x ? (flagOf(x) ?? '') : ''}</span>
                <span class="nm">{nameOf(x, id)}</span>
                {#if x?.stats?.speed != null}<span class="was">{mbpsText(x.stats.speed)} {t.unit}</span>{/if}
              </li>
            {/each}
          </ul>
          <div class="cost">
            <p>{t.cost(bytes(perServer), bytes(perServer * chosen.length))} {t.about(chosen.length * TURN_S)}</p>
            <p>{s.vpn.phase === 'on' ? t.viaVpn : t.withoutVpn}</p>
            {#each lowSubs as sub (sub.id)}
              <p class="warn"><Icon name="alert" size={13} />{t.trafficLeft(sub.name, bytes(sub.totalBytes! - sub.usedBytes!))}</p>
            {/each}
          </div>
        {/if}
      {:else}
        <span class="cap">{mode === 'done' ? t.results : t.queue}</span>
        <ol class="queue">
          {#each rows as row, i (row.id)}
            {@const r: SpeedResult | null = row.result}
            {@const active = run?.current === row.id}
            {@const isBest = mode === 'done' && best?.id === row.id}
            <li class:active class:done={r !== null} class:failed={r !== null && r.mbps === null} class:best={isBest} animate:flip={{ duration: 450 }}>
              <span class="rank" class:crown={isBest} title={isBest ? t.winner : undefined}>
                {#if isBest}<Icon name="crown" size={14} />{:else if mode === 'done' && r?.mbps != null}{i + 1}{/if}
              </span>
              <span class="flag">{row.server ? (flagOf(row.server) ?? '') : ''}</span>
              <span class="nm">{nameOf(row.server, row.id)}</span>
              {#if r}
                {#if r.mbps !== null}
                  <span class="pg" title={r.jitter !== null ? t.jitter(r.jitter) : undefined}>{t.pingMs(r.ping ?? 0)}</span>
                  <span class="bar"><i style:width="{(r.mbps / top) * 100}%"></i></span>
                  <span class="mb">{mbpsText(r.mbps)}</span>
                {:else}
                  <span class="err" title={r.error ?? undefined}><Icon name="alert" size={13} />{r.error}</span>
                {/if}
              {:else if active}
                <span class="live">
                  {#if phase === 'download' && sample}
                    <span class="mb">{mbpsText(sample.mbps)}</span>
                  {:else}
                    <span class="hop"><i></i><i></i><i></i></span>
                  {/if}
                </span>
              {:else}
                <span class="wait">{t.waiting}</span>
              {/if}
            </li>
          {/each}
        </ol>
      {/if}
    </div>
  </div>

  {#snippet actions()}
    {#if mode === 'running'}
      <Button variant="ghost" onclick={onclose}>{t.hide}</Button>
      <Button icon="halt" onclick={stop}>{t.stop}</Button>
    {:else if mode === 'done'}
      <Button variant="ghost" onclick={onclose}>{t.close}</Button>
      <Button variant="ghost" icon="refresh" onclick={() => (kept = null)}>{t.again}</Button>
      {#if best}
        {@const chosenNow = s.server.selected === best.id}
        <Button icon="check" disabled={chosenNow} onclick={() => daemon.selectServer(best.id)}>{chosenNow ? t.chosen : t.choose}</Button>
      {/if}
    {:else}
      <Button variant="ghost" onclick={onclose}>{t.close}</Button>
      <Button icon="gauge" busy={starting} disabled={chosen.length === 0 || s.busy !== null} onclick={start}>{t.start}</Button>
    {/if}
  {/snippet}
</Dialog>

<style>
  .body {
    display: grid;
    grid-template-columns: 300px 1fr;
    gap: var(--s5);
    min-height: 330px;
  }

  .dial {
    display: grid;
    align-content: start;
    justify-items: center;
    gap: var(--s3);
    padding: var(--s1) 0;
  }

  .big {
    font: 600 34px / 1 var(--font-display);
    letter-spacing: -0.02em;
    font-variant-numeric: tabular-nums;
  }

  .big small {
    margin-left: 3px;
    color: var(--muted);
    font-size: 14px;
    font-weight: 400;
  }

  .big.rest {
    color: var(--muted);
  }

  .unit {
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .under {
    display: grid;
    justify-items: center;
    gap: 6px;
    width: 280px;
    min-height: 92px;
  }

  .now {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    max-width: 100%;
    font-size: var(--fs-body);
    text-align: center;
  }

  .now .nm {
    min-width: 0;
    overflow: hidden;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .ph {
    flex: none;
    color: var(--muted);
  }

  .hint {
    color: var(--muted);
    font-size: var(--fs-cap);
    line-height: 1.4;
    text-align: center;
  }

  .pings {
    display: flex;
    align-items: center;
    gap: 5px;
    min-height: 16px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-variant-numeric: tabular-nums;
  }

  .dot {
    width: 7px;
    height: 7px;
    border: 1.5px solid var(--line);
    border-radius: 50%;
    transition:
      background 0.25s,
      border-color 0.25s,
      transform 0.25s;
  }

  .dot.got {
    border-color: var(--vpn);
    background: var(--vpn);
    transform: scale(1.15);
  }

  .dot.miss {
    border-color: var(--bad);
  }

  .prog {
    margin-left: 6px;
  }

  .spark {
    width: 260px;
    height: 34px;
  }

  .area {
    fill: color-mix(in srgb, var(--globe-ok-a) 16%, transparent);
  }

  .stroke {
    fill: none;
    stroke: var(--globe-ok-a);
    stroke-width: 1.6;
    stroke-linejoin: round;
    vector-effect: non-scaling-stroke;
  }

  .side {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    min-width: 0;
  }

  .cap {
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .empty {
    padding: var(--s4) 0;
    color: var(--muted);
  }

  .pick,
  .queue {
    display: grid;
    /* A long name gives way, not the numbers next to it. */
    grid-template-columns: minmax(0, 1fr);
    margin: 0;
    padding: 2px 0;
    list-style: none;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface-2);
    max-height: 230px;
    overflow-y: auto;
  }

  .pick li,
  .queue li {
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--s2);
    min-height: 34px;
    padding: 0 10px;
    border-top: 1px solid var(--line);
  }

  .pick li:first-child,
  .queue li:first-child {
    border-top-color: transparent;
  }

  .pick li {
    animation: slide 0.3s cubic-bezier(0.2, 0.8, 0.2, 1) both;
    animation-delay: calc(var(--i, 0) * 35ms);
  }

  @keyframes slide {
    from {
      opacity: 0;
      transform: translateX(8px);
    }
  }

  .flag {
    flex: none;
    width: 20px;
    font-size: 15px;
    line-height: 1;
    text-align: center;
  }

  .nm {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .was,
  .wait,
  .pg {
    flex: none;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-variant-numeric: tabular-nums;
  }

  .cost {
    display: grid;
    gap: 4px;
    color: var(--muted);
    font-size: var(--fs-cap);
    line-height: 1.4;
  }

  .cost .warn {
    display: flex;
    align-items: center;
    gap: 5px;
    color: var(--warn);
  }

  .rank {
    display: inline-flex;
    flex: none;
    justify-content: center;
    width: 16px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-variant-numeric: tabular-nums;
  }

  .queue li.active {
    background: var(--surface);
  }

  /* The server being measured: a light runs along its row. */
  .queue li.active::after {
    content: '';
    position: absolute;
    inset: 0;
    background: linear-gradient(90deg, transparent, color-mix(in srgb, var(--globe-ok-a) 12%, transparent), transparent) no-repeat;
    background-size: 40% 100%;
    animation: sweep 1.3s ease-in-out infinite;
    pointer-events: none;
  }

  @keyframes sweep {
    from {
      background-position: -60% 0;
    }
    to {
      background-position: 160% 0;
    }
  }

  .queue li.done {
    animation: land 0.45s cubic-bezier(0.2, 0.8, 0.2, 1);
  }

  @keyframes land {
    from {
      background: color-mix(in srgb, var(--text) 8%, transparent);
    }
  }

  .bar {
    position: relative;
    flex: none;
    width: 80px;
    height: 6px;
    overflow: hidden;
    border-radius: 3px;
    background: var(--line);
  }

  .bar i {
    position: absolute;
    inset: 0 auto 0 0;
    border-radius: 3px;
    background: linear-gradient(90deg, var(--globe-ok-a), var(--globe-ok-b));
    transition: width 0.6s cubic-bezier(0.22, 1, 0.36, 1);
    animation: grow 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
    transform-origin: left;
  }

  @keyframes grow {
    from {
      transform: scaleX(0);
    }
  }

  .mb {
    flex: none;
    width: 40px;
    font-weight: 600;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }

  .live {
    display: inline-flex;
    flex: none;
    align-items: center;
    justify-content: flex-end;
    min-width: 40px;
  }

  .hop {
    display: inline-flex;
    gap: 3px;
  }

  .hop i {
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--muted);
    animation: hop 0.9s ease-in-out infinite;
  }

  .hop i:nth-child(2) {
    animation-delay: 0.15s;
  }

  .hop i:nth-child(3) {
    animation-delay: 0.3s;
  }

  @keyframes hop {
    0%,
    70%,
    100% {
      opacity: 0.3;
      transform: translateY(0);
    }
    35% {
      opacity: 1;
      transform: translateY(-3px);
    }
  }

  .err {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 4px;
    max-width: 55%;
    overflow: hidden;
    color: var(--bad);
    font-size: var(--fs-cap);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .queue li.best {
    background: color-mix(in srgb, var(--vpn) 9%, var(--surface));
  }

  .rank.crown {
    color: var(--vpn);
    animation: crown 0.6s cubic-bezier(0.2, 0.8, 0.2, 1.4) 0.3s both;
  }

  @keyframes crown {
    from {
      opacity: 0;
      transform: translateY(-6px) rotate(-20deg) scale(0.6);
    }
  }
</style>
