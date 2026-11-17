<script lang="ts">
  import { onMount } from 'svelte';
  import Button from '../components/Button.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Icon from '../components/Icon.svelte';
  import { ru } from '../i18n/ru';
  import type { DiagStep, DiagStepName } from '../proto';
  import { daemon } from '../store.svelte';

  // The connection check: the daemon walks from the cable to a site through
  // the VPN and says at each step what it found. Where it breaks is the
  // answer; its advice is what to do.
  let { onclose }: { onclose: () => void } = $props();

  const t = ru.diag;
  const ORDER: DiagStepName[] = ['network', 'internet', 'dns', 'engine', 'server', 'site'];

  let steps = $state<Partial<Record<DiagStepName, DiagStep>>>({});
  let running = $state(false);
  let error = $state<string | null>(null);

  $effect(() => daemon.watchDiag((step) => (steps = { ...steps, [step.step]: step })));

  async function run(): Promise<void> {
    running = true;
    error = null;
    steps = {};
    try {
      const res = await daemon.call('diag.run');
      // The answer is the whole truth; a notification may have been lost.
      steps = Object.fromEntries(res.steps.map((s) => [s.step, s]));
    } catch (err) {
      error = (err as Error).message;
    } finally {
      running = false;
    }
  }

  onMount(() => {
    void run();
  });

  const verdict = $derived.by(() => {
    if (running || error) return null;
    const list = ORDER.map((name) => steps[name]).filter((s): s is DiagStep => s !== undefined);
    if (list.length < ORDER.length) return null;
    const broken = list.find((s) => s.status === 'fail');
    if (broken) return { tone: 'bad' as const, text: t.broken(t.steps[broken.step]) };
    if (list.some((s) => s.status === 'skip')) return { tone: 'off' as const, text: t.offOk };
    return { tone: 'ok' as const, text: t.allOk };
  });

  const ICONS = { ok: 'check', fail: 'alert', skip: 'minimize', running: 'pulse' } as const;
</script>

<Dialog title={t.title} width={520} {onclose}>
  {#snippet children()}
    <p class="lead">{t.lead}</p>
    <ol class="steps" aria-busy={running}>
      {#each ORDER as name (name)}
        {@const step = steps[name]}
        <li class={step?.status ?? 'waiting'}>
          <span class="ico" aria-hidden="true">
            {#if step?.status === 'running'}
              <span class="spin"></span>
            {:else if step}
              <Icon name={ICONS[step.status]} size={16} />
            {:else}
              <span class="dot"></span>
            {/if}
          </span>
          <div class="body">
            <div class="head">
              <b>{t.steps[name]}</b>
              <span class="status">{step ? t.status[step.status] : t.waiting}</span>
            </div>
            {#if step}<p>{step.text}</p>{/if}
            {#if step?.advice}<p class="advice">{step.advice}</p>{/if}
          </div>
        </li>
      {/each}
    </ol>
    {#if error}
      <p class="verdict bad" role="alert">{error}</p>
    {:else if verdict}
      <p class="verdict {verdict.tone}" role="status">{verdict.text}</p>
    {/if}
  {/snippet}
  {#snippet actions()}
    <Button variant="ghost" onclick={onclose}>{t.close}</Button>
    <Button icon="refresh" busy={running} onclick={run}>{t.again}</Button>
  {/snippet}
</Dialog>

<style>
  .lead {
    color: var(--muted);
    margin-bottom: var(--s3);
  }

  .steps {
    display: grid;
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
  }

  li {
    display: grid;
    grid-template-columns: 20px 1fr;
    gap: 10px;
    padding: 9px 12px;
  }

  li + li {
    border-top: 1px solid var(--line);
  }

  /* The smallest window: six steps and the buttons fit without scrolling. */
  @media (max-height: 620px) {
    .lead {
      margin-bottom: var(--s2);
    }

    li {
      padding-block: 6px;
    }
  }

  .ico {
    display: grid;
    place-items: center;
    height: 18px;
    color: var(--muted);
  }

  .ok .ico {
    color: var(--vpn);
  }

  .fail .ico {
    color: var(--bad);
  }

  .head {
    display: flex;
    justify-content: space-between;
    gap: var(--s3);
  }

  b {
    font-weight: 600;
  }

  .status {
    color: var(--muted);
    font-size: var(--fs-cap);
    white-space: nowrap;
  }

  .fail .status {
    color: var(--bad);
  }

  .waiting b,
  .skip b {
    color: var(--muted);
  }

  .body p {
    color: var(--muted);
    margin-top: 2px;
  }

  .fail .body p:not(.advice) {
    color: var(--text);
  }

  .advice {
    padding-left: 10px;
    border-left: 2px solid var(--line);
  }

  .fail .advice {
    border-left-color: var(--bad);
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--line);
  }

  .spin {
    width: 14px;
    height: 14px;
    border: 2px solid var(--line);
    border-top-color: var(--text);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .spin {
      animation: none;
      border-top-color: var(--muted);
    }
  }

  .verdict {
    margin-top: var(--s3);
    font-weight: 600;
  }

  .verdict.ok {
    color: var(--vpn);
  }

  .verdict.bad {
    color: var(--bad);
  }
</style>
