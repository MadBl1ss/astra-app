<script lang="ts">
  import Card from '../components/Card.svelte';
  import Icon, { type IconName } from '../components/Icon.svelte';
  import Segments from '../components/Segments.svelte';
  import { ru } from '../i18n/ru';
  import { theme, type ThemeChoice } from '../theme.svelte';

  // A placeholder for the four screens of the next stage. Settings already
  // carries the one setting that belongs to the window itself: the theme.
  let { section }: { section: 'servers' | 'rules' | 'journal' | 'settings' } = $props();

  const icons: Record<typeof section, IconName> = {
    servers: 'servers',
    rules: 'rules',
    journal: 'journal',
    settings: 'settings',
  };

  const themes: readonly { value: ThemeChoice; label: string }[] = [
    { value: 'system', label: ru.settings.themeSystem },
    { value: 'dark', label: ru.settings.themeDark },
    { value: 'light', label: ru.settings.themeLight },
  ];
</script>

<div class="soon">
  <h1>{ru.nav[section]}</h1>

  {#if section === 'settings'}
    <div class="group">
      <h2>{ru.settings.look}</h2>
      <Card caption={ru.settings.theme}>
        <div class="theme">
          <Segments options={themes} value={theme.choice} label={ru.settings.theme} onchange={(choice) => theme.set(choice)} />
        </div>
      </Card>
    </div>
  {/if}

  <div class="note">
    <span class="ico"><Icon name={icons[section]} size={22} /></span>
    <div>
      <b>{ru.soon.title}</b>
      <p>{ru.soon[section]}</p>
    </div>
  </div>
</div>

<style>
  .soon {
    display: flex;
    flex-direction: column;
    gap: var(--s5);
  }

  h1 {
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  h2 {
    margin-bottom: var(--s2);
    font-size: var(--fs-head);
    font-weight: 600;
  }

  .theme {
    max-width: 380px;
  }

  .note {
    display: flex;
    gap: var(--s3);
    max-width: 520px;
    padding: var(--s3) var(--s4);
    border: 1px dashed var(--line);
    border-radius: var(--r-card);
    color: var(--muted);
  }

  .note b {
    color: var(--text);
    font-weight: 600;
  }

  .ico {
    display: flex;
    padding-top: 1px;
  }
</style>
