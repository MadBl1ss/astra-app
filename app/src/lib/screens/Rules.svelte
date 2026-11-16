<script lang="ts" module>
  // The tab the screen was left on.
  let lastTab: 'vpn' | 'direct' = 'vpn';
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import Button from '../components/Button.svelte';
  import Chip from '../components/Chip.svelte';
  import Empty from '../components/Empty.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import Loading from '../components/Loading.svelte';
  import Select, { type SelectItem } from '../components/Select.svelte';
  import Tabs from '../components/Tabs.svelte';
  import { ago, count } from '../format';
  import { ru } from '../i18n/ru';
  import type { RuleKind, RuleListName, Rules, State } from '../proto';
  import { listName, ruleCount, ruleGroups, type RuleRef } from '../rules';
  import { win } from '../shell/window.svelte';
  import { daemon } from '../store.svelte';
  import LiveConns from './rules/LiveConns.svelte';
  import ProcessesDialog from './rules/ProcessesDialog.svelte';

  let { state: s }: { state: State } = $props();

  const t = ru.rules;

  let rules = $state.raw<Rules | null>(null);
  let failed = $state<string | null>(null);
  let tab = $state<RuleListName>(lastTab);
  $effect(() => {
    lastTab = tab;
  });

  async function load(): Promise<void> {
    try {
      rules = await daemon.call('rules.get');
      failed = null;
    } catch (err) {
      if (!rules) failed = (err as Error).message;
    }
  }

  // The lists change from here, and also from strelkactl or another window:
  // every change is a journal line of the "rules" kind, so a new one of
  // those is the cue to read them again.
  let seen = 0;
  $effect(() => {
    const top = daemon.journal[0];
    untrack(() => {
      if (!top || top.id <= seen) return;
      const first = seen === 0;
      seen = top.id;
      if (first || top.cat === 'rules') void load();
    });
  });

  // ---- adding ----

  let value = $state('');
  let kind = $state<RuleKind | 'auto'>('auto');
  let addError = $state<string | null>(null);
  let addNote = $state<string | null>(null);
  let adding = $state(false);
  let fresh = $state<{ list: RuleListName; kind: RuleKind; value: string } | null>(null);
  let freshTimer: ReturnType<typeof setTimeout> | undefined;

  const kinds: SelectItem[] = (['auto', 'process', 'suffix', 'domain', 'keyword', 'ip'] as const).map((k) => ({
    value: k,
    label: t.kinds[k],
  }));

  /** Adds a rule and says how the daemon understood it. */
  async function add(list: RuleListName, raw: string, as?: RuleKind): Promise<boolean> {
    adding = true;
    addError = null;
    try {
      const res = await daemon.call('rules.add', as ? { list, value: raw, kind: as } : { list, value: raw });
      addNote = t.added(listName(list), t.kindWord[res.kind], res.value);
      fresh = { list, kind: res.kind, value: res.value };
      clearTimeout(freshTimer);
      freshTimer = setTimeout(() => (fresh = null), 2000);
      tab = list;
      await load();
      return true;
    } catch (err) {
      addError = (err as Error).message;
      return false;
    } finally {
      adding = false;
    }
  }

  async function addTyped(): Promise<void> {
    if (!value.trim()) return;
    if (await add(tab, value, kind === 'auto' ? undefined : kind)) value = '';
  }

  async function remove(list: RuleListName, rule: RuleRef): Promise<void> {
    const done = await daemon.rpc('rules.remove', { list, kind: rule.kind, value: rule.value });
    if (done === undefined) return;
    addError = null;
    addNote = t.removed(listName(list), rule.value);
    await load();
  }

  function move(list: RuleListName, rule: RuleRef): void {
    // The daemon takes a value out of the other list when it is added.
    void add(list === 'vpn' ? 'direct' : 'vpn', rule.value, rule.kind);
  }

  // A rule made from a live connection: said under the field like any other.
  function fromLive(list: RuleListName, rule: RuleRef, _what: string): void {
    void add(list, rule.value, rule.kind).then((ok) => {
      if (ok) addNote = `${addNote} ${t.newConns}`;
    });
  }

  // ---- what is shown ----

  const current = $derived(rules ? rules[tab] : null);
  const groups = $derived(current ? ruleGroups(current, value) : []);
  const total = $derived(current ? ruleCount(current) : 0);
  const autoTotal = $derived(rules ? rules.auto.reduce((n, a) => n + Math.max(0, a.count), 0) : 0);
  const autoAt = $derived(
    rules?.auto
      .map((a) => a.updatedAt)
      .filter((x): x is string => x !== null)
      .sort()
      .at(-1) ?? null,
  );
  const have = $derived(new Set((current?.processes ?? []).map((p) => p.toLowerCase())));

  let picking = $state(false);

  // ---- where will it go ----

  let target = $state('');
  let answer = $state<{ route: 'vpn' | 'direct'; because: string } | null>(null);
  let checkError = $state<string | null>(null);
  let checking = $state(false);

  async function check(): Promise<void> {
    if (!target.trim()) return;
    checking = true;
    checkError = null;
    try {
      answer = await daemon.call('rules.check', { target: target.trim() });
    } catch (err) {
      answer = null;
      checkError = (err as Error).message;
    } finally {
      checking = false;
    }
  }

  // ---- live connections, only while the screen is looked at ----

  let visible = $state(document.visibilityState === 'visible');
  $effect(() => {
    const update = () => (visible = document.visibilityState === 'visible');
    document.addEventListener('visibilitychange', update);
    return () => document.removeEventListener('visibilitychange', update);
  });
  $effect(() => {
    if (visible && win.shown) return daemon.watchConnections();
  });
</script>

<div class="screen">
  <h1>{ru.nav.rules}</h1>

  <div class="cols">
    <section class="lists" aria-label={t.lists}>
      <Tabs
        label={t.lists}
        panel="rules-panel"
        value={tab}
        tabs={[
          { value: 'vpn', label: t.vpn, count: rules ? ruleCount(rules.vpn) : undefined },
          { value: 'direct', label: t.direct, count: rules ? ruleCount(rules.direct) : undefined },
        ]}
        onchange={(next) => {
          tab = next;
          addError = null;
          addNote = null;
        }}
      />

      <div class="add">
        <div class="line">
          <div class="grow">
            <Field
              label={t.add}
              hideLabel
              icon="plus"
              placeholder={t.addPlaceholder}
              bind:value
              error={addError}
              note={addNote ?? (value.trim() && groups.length === 0 && total > 0 ? t.noMatch : null)}
              disabled={adding}
              onenter={addTyped}
              onkeydown={() => {
                addNote = null;
                addError = null;
              }}
            />
          </div>
          <Button busy={adding} disabled={!value.trim()} onclick={addTyped}>{t.addButton}</Button>
        </div>
        <div class="line">
          <span class="cap">{t.kind}</span>
          <div class="kind">
            <Select items={kinds} value={kind} label={t.kind} placeholder={t.kinds.auto} onchange={(k) => (kind = k as RuleKind | 'auto')} />
          </div>
          <span class="spacer"></span>
          <Button variant="ghost" icon="app" onclick={() => (picking = true)}>{t.fromRunning}</Button>
        </div>
      </div>

      <div class="panel" id="rules-panel" role="tabpanel" aria-labelledby="rules-panel-tab-{tab}">
        {#if tab === 'vpn' && s.vpn.mode === 'global'}
          <p class="note"><Icon name="info" size={14} />{t.globalNote}</p>
        {/if}
        {#if rules === null && !failed}
          <Loading label={t.loading} />
        {:else if failed}
          <Empty compact icon="alert" title={failed} />
        {:else if total === 0}
          <Empty compact icon="rules" title={t.emptyList} text={tab === 'vpn' ? t.emptyVpnText : t.emptyDirectText} />
        {:else}
          {#each groups as group (group.key)}
            <div class="group">
              <h3>{group.title} <span class="n">{group.rules.length}</span></h3>
              <ul class="chips">
                {#each group.rules as rule (rule.kind + rule.value)}
                  <Chip
                    label={rule.value}
                    tag={rule.kind === 'domain' ? t.exact : undefined}
                    tagTitle={rule.kind === 'domain' ? t.exactTitle(rule.value) : undefined}
                    fresh={fresh?.list === tab && fresh.kind === rule.kind && fresh.value === rule.value}
                    moveLabel={t.moveTo(listName(tab === 'vpn' ? 'direct' : 'vpn'))}
                    removeLabel={t.remove(rule.value)}
                    onmove={() => move(tab, rule)}
                    onremove={() => remove(tab, rule)}
                  />
                {/each}
              </ul>
            </div>
          {/each}
        {/if}
      </div>

      {#if rules && rules.auto.length > 0}
        <p class="auto" title={t.autoNote}>
          <Icon name="globe" size={15} />
          <span><b>{t.auto}.</b> {t.autoLine(count(autoTotal), ago(autoAt, daemon.now))}</span>
        </p>
      {/if}

      <div class="check">
        <div class="grow">
          <Field
            label={t.check}
            icon="route"
            placeholder={t.checkPlaceholder}
            bind:value={target}
            error={checkError}
            onenter={check}
            onkeydown={() => (answer = null)}
          />
        </div>
        <Button busy={checking} disabled={!target.trim()} onclick={check}>{t.checkButton}</Button>
      </div>
      {#if answer}
        <p class="answer" aria-live="polite">
          <b class={answer.route}>{answer.route === 'vpn' ? t.checkVpn : t.checkDirect}.</b>
          {answer.because}
        </p>
      {/if}
    </section>

    <LiveConns connections={daemon.connections} vpnOn={s.vpn.phase === 'on'} onrule={fromLive} />
  </div>
</div>

{#if picking}
  <ProcessesDialog
    list={tab}
    listTitle={listName(tab)}
    {have}
    onadd={(name) => add(tab, name, 'process')}
    onclose={() => (picking = false)}
  />
{/if}

<style>
  .screen {
    display: flex;
    flex-direction: column;
    gap: var(--s3);
    height: 100%;
    min-height: 0;
  }

  h1 {
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  .cols {
    display: grid;
    flex: 1;
    grid-template-columns: minmax(0, 1.08fr) minmax(0, 1fr);
    gap: var(--s5);
    min-height: 0;
  }

  .lists {
    display: flex;
    flex-direction: column;
    gap: var(--s3);
    min-height: 0;
  }

  .add {
    display: grid;
    gap: var(--s2);
  }

  .line,
  .check {
    display: flex;
    align-items: flex-start;
    gap: var(--s2);
  }

  .line:last-child {
    align-items: center;
  }

  .cap {
    color: var(--muted);
    font-size: var(--fs-cap);
    white-space: nowrap;
  }

  .spacer {
    flex: 1;
  }

  .add :global(.btn),
  .check :global(.btn) {
    min-height: 31px;
  }

  .grow {
    flex: 1 1 200px;
    min-width: 0;
  }

  .kind {
    flex: 0 1 180px;
    min-width: 0;
  }

  .panel {
    flex: 1;
    min-height: 90px;
    overflow-y: auto;
    padding-right: 4px;
    overscroll-behavior: contain;
  }

  .note {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin-bottom: var(--s3);
    padding: 7px 10px;
    border: 1px dashed var(--line);
    border-radius: var(--r-ctl);
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .note :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .group + .group {
    margin-top: var(--s3);
  }

  h3 {
    margin-bottom: 6px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }

  .n {
    margin-left: 2px;
    opacity: 0.8;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .auto {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface);
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .auto :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .auto b {
    color: var(--text);
    font-weight: 600;
  }

  .answer {
    margin-top: calc(-1 * var(--s1));
    color: var(--muted);
  }

  .answer b {
    font-weight: 600;
  }

  .answer .vpn {
    color: var(--vpn);
  }

  .answer .direct {
    color: var(--text);
  }
</style>
