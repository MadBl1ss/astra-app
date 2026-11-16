<script lang="ts" module>
  import type { FilterName } from '../journal';

  // The filter the screen was left with.
  let lastFilter: FilterName = 'all';
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import Button from '../components/Button.svelte';
  import Empty from '../components/Empty.svelte';
  import Icon from '../components/Icon.svelte';
  import IconButton from '../components/IconButton.svelte';
  import Loading from '../components/Loading.svelte';
  import Segments from '../components/Segments.svelte';
  import { clock } from '../format';
  import { ru } from '../i18n/ru';
  import { FILTERS, complete, feed, fits, incidentView } from '../journal';
  import type { JournalEntry, State } from '../proto';
  import { fileName, report } from '../report.svelte';
  import { daemon } from '../store.svelte';
  import { inWindow } from '../transport';

  let { state: s }: { state: State } = $props();

  const t = ru.journal;
  const PAGE = 100;

  let filter = $state<FilterName>(lastFilter);
  let entries = $state.raw<JournalEntry[]>([]);
  let more = $state(false);
  let loading = $state(true);
  let failed = $state<string | null>(null);
  // Answers to a filter the user has already left are dropped.
  let generation = 0;

  async function load(first: boolean): Promise<void> {
    const gen = first ? ++generation : generation;
    loading = true;
    const before = first ? undefined : entries[entries.length - 1]?.id;
    try {
      const page = await daemon.call('journal.list', { limit: PAGE, before, filter: FILTERS[filter] });
      if (gen !== generation) return;
      const known = new Set(first ? [] : entries.map((e) => e.id));
      entries = [...(first ? [] : entries), ...page.entries.filter((e) => !known.has(e.id))];
      more = page.more;
      failed = null;
    } catch (err) {
      if (gen === generation) failed = (err as Error).message;
    } finally {
      if (gen === generation) loading = false;
    }
  }

  $effect(() => {
    lastFilter = filter;
    untrack(() => {
      entries = [];
      more = false;
      void load(true);
    });
  });

  // New lines arrive through the store; those that pass the filter go on top.
  $effect(() => {
    const live = daemon.journal;
    untrack(() => {
      if (loading && entries.length === 0) return;
      const top = entries[0]?.id ?? 0;
      const fresh = live.filter((e) => e.id > top && fits(e, FILTERS[filter]));
      if (fresh.length > 0) entries = [...fresh, ...entries];
      for (const e of live) {
        if (e.incident && details[e.incident] && !details[e.incident]!.some((d) => d.id === e.id)) {
          details[e.incident] = [e, ...details[e.incident]!];
        }
      }
    });
  });

  // ---- incidents ----

  const openIds = $derived(new Set(s.incidents.map((i) => i.id)));
  // Every line of an incident, fetched when the loaded ones do not tell
  // its whole story. One request at a time: the daemon keeps a short queue
  // per client and a page can hold dozens of incidents.
  const details = $state<Record<string, JournalEntry[]>>({});
  let expanded = $state<Record<string, boolean>>({});
  const queue: string[] = [];
  let fetching = false;

  function want(id: string): void {
    if (details[id] || queue.includes(id)) return;
    queue.push(id);
    if (!fetching) void drain();
  }

  async function drain(): Promise<void> {
    fetching = true;
    while (queue.length > 0) {
      const id = queue[0]!;
      try {
        const page = await daemon.call('journal.list', { limit: 500, filter: { incident: id } });
        details[id] = page.entries;
      } catch {
        // Shown with what is loaded; the next look asks again.
        queue.length = 0;
        break;
      }
      queue.shift();
    }
    fetching = false;
  }

  const days = $derived(feed(entries, daemon.now));

  $effect(() => {
    for (const day of days) {
      for (const item of day.items) {
        if (item.kind !== 'incident') continue;
        const open = openIds.has(item.id);
        if (expanded[item.id] || !complete(item.entries, open)) untrack(() => want(item.id));
      }
    }
  });

  function linesOf(id: string, loaded: JournalEntry[]): JournalEntry[] {
    return details[id] ?? loaded;
  }

  // ---- paging as the end comes into view ----

  let scroller: HTMLDivElement | undefined = $state();
  let sentinel: HTMLDivElement | undefined = $state();
  $effect(() => {
    if (!sentinel || !scroller) return;
    const io = new IntersectionObserver(
      ([hit]) => {
        if (hit?.isIntersecting && more && !loading) void load(false);
      },
      { root: scroller, rootMargin: '240px' },
    );
    io.observe(sentinel);
    return () => io.disconnect();
  });

  const filters = [
    { value: 'all', label: t.filters.all },
    { value: 'problems', label: t.filters.problems },
    { value: 'servers', label: t.filters.servers },
    { value: 'system', label: t.filters.system },
  ] as const;

  function tone(level: JournalEntry['level']): string {
    return level === 'critical' ? 'bad' : level === 'problem' ? 'warn' : '';
  }
</script>

<div class="screen">
  <header class="top">
    <h1>{ru.nav.journal}</h1>
    <div class="filter"><Segments options={filters} value={filter} label={t.filter} onchange={(f) => (filter = f)} /></div>
    <Button icon="report" busy={report.busy} title={t.reportNote} onclick={() => report.make()}>
      {report.busy ? t.reporting : t.report}
    </Button>
  </header>

  {#if report.path}
    <div class="done" role="status">
      <Icon name="report" size={16} />
      <div class="grow">
        <b>{t.reportDone}: {fileName(report.path)}</b>
        <span class="mono path" title={report.path}>{report.path}</span>
      </div>
      {#if inWindow}<Button variant="ghost" icon="folder" onclick={() => report.reveal()}>{t.reportShow}</Button>{/if}
      <IconButton icon="close" label={t.reportClose} onclick={() => report.dismiss()} />
    </div>
  {/if}

  <div class="feed" bind:this={scroller}>
    {#if loading && entries.length === 0}
      <Loading label={t.loading} rows={8} />
    {:else if failed && entries.length === 0}
      <Empty icon="alert" title={failed} />
    {:else if entries.length === 0}
      {#if filter === 'all'}
        <Empty icon="journal" title={t.empty} text={t.emptyText} />
      {:else}
        <Empty icon="journal" title={t.emptyFilter} text={t.emptyFilterText} />
      {/if}
    {:else}
      {#each days as day (day.key)}
        <section class="day">
          <h2>{day.title}</h2>
          <ul>
            {#each day.items as item (item.key)}
              {#if item.kind === 'entry'}
                <li class="row {tone(item.entry.level)}">
                  <span class="time">{clock(item.entry.t)}</span>
                  <span class="text">{item.entry.text}</span>
                </li>
              {:else}
                {@const lines = linesOf(item.id, item.entries)}
                {@const v = incidentView(lines, openIds.has(item.id), daemon.now)}
                {@const isOpen = expanded[item.id] ?? false}
                <li class="incident {tone(v.level)}" class:ongoing={!v.ended}>
                  <button
                    type="button"
                    class="row head"
                    aria-expanded={isOpen}
                    aria-label={t.details(v.start.text)}
                    onclick={() => (expanded[item.id] = !isOpen)}
                  >
                    <span class="time">{clock(v.start.t)}</span>
                    <span class="text">{v.start.text}</span>
                    <span class="meta">{v.meta}</span>
                    <span class="chev" class:open={isOpen}><Icon name="chevron" size={14} /></span>
                  </button>
                  {#if isOpen}
                    <ol class="lines">
                      {#each [...lines].reverse() as line (line.id)}
                        <li class={tone(line.level)}>
                          <span class="time">{clock(line.t)}</span>
                          <span class="text">{line.text}</span>
                        </li>
                      {/each}
                    </ol>
                  {/if}
                </li>
              {/if}
            {/each}
          </ul>
        </section>
      {/each}
      <div class="tail" bind:this={sentinel}>
        {#if more}
          <Button variant="ghost" busy={loading} onclick={() => load(false)}>{t.more}</Button>
        {:else}
          <span>{t.end}</span>
        {/if}
      </div>
    {/if}
  </div>
</div>

<style>
  .screen {
    display: flex;
    flex-direction: column;
    gap: var(--s3);
    height: 100%;
    min-height: 0;
  }

  .top {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--s2) var(--s4);
  }

  h1 {
    flex: 1;
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  .filter {
    width: 360px;
  }

  .done {
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: 8px 8px 8px 12px;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
    animation: appear var(--slow) ease-out;
  }

  @keyframes appear {
    from {
      opacity: 0;
      transform: translateY(-4px);
    }
  }

  .grow {
    display: grid;
    flex: 1;
    min-width: 0;
  }

  .done b {
    font-weight: 600;
  }

  .path {
    overflow: hidden;
    color: var(--muted);
    font-size: var(--fs-cap);
    text-overflow: ellipsis;
    white-space: nowrap;
    user-select: text;
  }

  .feed {
    flex: 1;
    min-height: 0;
    margin-right: -14px;
    padding-right: 14px;
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  .day + .day {
    margin-top: var(--s4);
  }

  h2 {
    position: sticky;
    z-index: 1;
    top: 0;
    padding: 4px 0;
    background: var(--bg);
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 600;
  }

  ul,
  ol {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .row {
    display: flex;
    align-items: baseline;
    gap: var(--s3);
    min-height: 31px;
    padding: 6px 0;
    border-top: 1px solid var(--line);
  }

  .time {
    flex: none;
    width: 40px;
    color: var(--muted);
  }

  /* The journal is read, not scanned: long lines wrap instead of hiding. */
  .text {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
    user-select: text;
  }

  /* A problem line is marked with a bar as well as a colour. */
  .warn > .text,
  .warn > .head .text,
  .lines .warn .text {
    padding-left: var(--s2);
    border-left: 2px solid var(--warn);
  }

  .bad > .text,
  .bad > .head .text,
  .lines .bad .text {
    padding-left: var(--s2);
    border-left: 2px solid var(--bad);
  }

  .incident .head {
    width: 100%;
    border-right: 0;
    border-bottom: 0;
    border-left: 0;
    background: transparent;
    text-align: left;
  }

  .incident .head:hover .text {
    text-decoration: underline;
    text-decoration-color: var(--line);
    text-underline-offset: 3px;
  }

  .head:focus-visible {
    outline-offset: -2px;
  }

  .incident .head .text {
    user-select: none;
  }

  .meta {
    flex: none;
    color: var(--muted);
    font-size: var(--fs-cap);
    white-space: nowrap;
  }

  .ongoing .meta {
    color: var(--warn);
  }

  .chev {
    display: flex;
    flex: none;
    align-self: center;
    color: var(--muted);
    transform: rotate(-90deg);
    transition: transform var(--fast);
  }

  .chev.open {
    transform: none;
  }

  .lines {
    margin: 0 0 6px 52px;
    padding: 2px 0 2px 12px;
    border-left: 1px solid var(--line);
  }

  .lines li {
    display: flex;
    align-items: baseline;
    gap: var(--s3);
    padding: 3px 0;
    color: var(--muted);
  }

  .lines .text {
    color: var(--text);
  }

  .tail {
    display: flex;
    justify-content: center;
    padding: var(--s4) 0 var(--s2);
    color: var(--muted);
    font-size: var(--fs-cap);
  }
</style>
