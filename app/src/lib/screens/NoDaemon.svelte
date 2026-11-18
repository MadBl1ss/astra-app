<script lang="ts">
  import Button from '../components/Button.svelte';
  import Icon from '../components/Icon.svelte';
  import { ru } from '../i18n/ru';
  import { daemon } from '../store.svelte';
  import { updates } from '../update.svelte';

  // Two cases share the screen: the daemon cannot be reached, or it can but
  // speaks another protocol version. Either way nothing else works.
  const t = ru.noDaemon;
  const outdated = $derived(daemon.outdated);
  const trying = $derived(daemon.link === 'connecting');
</script>

<div class="wrap">
  <div class="box" role="alert">
    <span class="ico"><Icon name={outdated ? 'info' : 'plug'} size={28} /></span>
    {#if outdated}
      <h1>{t.outdatedTitle}</h1>
      <p>{updates.available ? t.outdatedUpdate : t.outdatedBody}</p>
      <p class="fine mono">{t.versions(outdated.app, outdated.daemon)}</p>
      {#if updates.available}
        <Button icon="download" onclick={() => updates.show()}>{ru.update.outdated(updates.available.version)}</Button>
      {/if}
    {:else}
      <h1>{t.title}</h1>
      <p>{t.body}</p>
      <ul>
        {#each t.steps as step (step)}
          <li>{step}</li>
        {/each}
      </ul>
      <Button disabled={trying} onclick={() => daemon.retry()}>{trying ? t.retrying : t.retry}</Button>
    {/if}
  </div>
</div>

<style>
  .wrap {
    display: grid;
    place-items: center;
    height: 100%;
    padding: var(--s6);
  }

  .box {
    display: grid;
    justify-items: start;
    gap: var(--s3);
    max-width: 460px;
  }

  .ico {
    display: flex;
    color: var(--warn);
  }

  h1 {
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  p {
    color: var(--muted);
  }

  ul {
    display: grid;
    gap: 6px;
    margin: 0 0 var(--s2);
    padding-left: 18px;
    color: var(--muted);
  }

  .fine {
    font-size: var(--fs-cap);
    user-select: text;
  }
</style>
