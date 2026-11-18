<script lang="ts">
  import Button from '../components/Button.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Icon, { type IconName } from '../components/Icon.svelte';
  import { ru } from '../i18n/ru';
  import type { Mode } from '../proto';

  // What the two modes send where, side by side, with a way to switch from
  // here: the choice is easier to make while reading about it.
  let {
    mode,
    onmode,
    onrules,
    onclose,
  }: {
    mode: Mode;
    onmode: (mode: Mode) => void;
    onrules: () => void;
    onclose: () => void;
  } = $props();

  const t = ru.modeDialog;
</script>

{#snippet head(value: Mode, icon: IconName, name: string, tag: string | null)}
  <header>
    <span class="badge"><Icon name={icon} size={17} /></span>
    <h3>{name}</h3>
    {#if tag}<span class="tag">{tag}</span>{/if}
    <span class="grow"></span>
    {#if mode === value}
      <span class="now"><Icon name="check" size={14} />{t.now}</span>
    {:else}
      <Button variant="ghost" onclick={() => onmode(value)}>{t.turnOn}</Button>
    {/if}
  </header>
{/snippet}

<Dialog title={t.title} width={720} {onclose}>
  <p class="intro">{t.intro}</p>
  <div class="modes">
    <section class="mode" class:on={mode === 'rule'} aria-label={ru.quick.modeRule}>
      {@render head('rule', 'route', ru.quick.modeRule, t.advised)}
      <p class="lead">{t.smartLead}</p>
      <h4><span class="dot vpn"></span>{t.vpn}</h4>
      <ul>
        {#each t.smartVpn as line (line)}<li>{line}</li>{/each}
      </ul>
      <h4><span class="dot direct"></span>{t.direct}</h4>
      <p>{t.smartDirect}</p>
    </section>

    <section class="mode" class:on={mode === 'global'} aria-label={ru.quick.modeGlobal}>
      {@render head('global', 'shield', ru.quick.modeGlobal, null)}
      <p class="lead">{t.globalLead}</p>
      <h4>{t.when}</h4>
      <ul>
        {#each t.globalWhen as line (line)}<li>{line}</li>{/each}
      </ul>
      <h4>{t.cost}</h4>
      <p>{t.globalCost}</p>
    </section>
  </div>
  <p class="foot"><Icon name="info" size={14} /><span>{t.footnote}</span></p>
  {#snippet actions()}
    <Button variant="ghost" icon="rules" onclick={onrules}>{t.rules}</Button>
    <Button onclick={onclose}>{t.close}</Button>
  {/snippet}
</Dialog>

<style>
  .intro {
    margin-top: -4px;
    color: var(--muted);
  }

  .modes {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--s3);
  }

  .mode {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 14px 14px;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--bg);
    line-height: 1.45;
  }

  /* The mode in use: the neutral accent of the interface, not a state colour. */
  .mode.on {
    border-color: color-mix(in srgb, var(--text) 50%, var(--line));
  }

  header {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 28px;
    margin-bottom: 2px;
  }

  .badge {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface-2);
  }

  h3 {
    font-size: var(--fs-head);
    font-weight: 600;
  }

  .tag {
    padding: 1px 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    color: var(--muted);
    font-size: 11px;
  }

  .grow {
    flex: 1;
  }

  .now {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .lead {
    font-weight: 500;
  }

  h4 {
    display: flex;
    align-items: center;
    gap: 7px;
    margin: 4px 0 0;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }

  /* The colours of the two tracks on Home: the same words, the same marks. */
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .dot.vpn {
    background: var(--vpn);
  }

  .dot.direct {
    background: var(--direct);
  }

  ul {
    display: grid;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  li {
    position: relative;
    padding-left: 14px;
  }

  li::before {
    content: '';
    position: absolute;
    top: 0.62em;
    left: 2px;
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--muted);
  }

  .foot {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    color: var(--muted);
    font-size: var(--fs-cap);
    line-height: 1.4;
  }

  .foot :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  @media (max-width: 760px) {
    .modes {
      grid-template-columns: 1fr;
    }
  }

  /* The smallest window: everything up to the buttons without scrolling. */
  @media (max-height: 620px) {
    .intro {
      display: none;
    }

    .mode {
      gap: 6px;
      padding: 10px 12px 12px;
      line-height: 1.35;
    }

    ul {
      gap: 4px;
    }
  }
</style>
