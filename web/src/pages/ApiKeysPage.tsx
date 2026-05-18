import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { parseDocument } from 'yaml';
import { ConfigSection } from '@/components/config/ConfigSection';
import { DiffModal } from '@/components/config/DiffModal';
import { ApiKeysCardEditor } from '@/components/config/VisualConfigEditorBlocks';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { IconCheck, IconKey, IconRefreshCw } from '@/components/ui/icons';
import { useVisualConfig } from '@/hooks/useVisualConfig';
import { configFileApi } from '@/services/api/configFile';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import styles from './ConfigPage.module.scss';

export function ApiKeysPage() {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const showConfirmation = useNotificationStore((state) => state.showConfirmation);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);

  const {
    visualValues,
    visualDirty,
    visualParseError,
    loadVisualValuesFromYaml,
    applyAuthChangesToYaml,
    setVisualValues,
  } = useVisualConfig();

  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [diffModalOpen, setDiffModalOpen] = useState(false);
  const [serverYaml, setServerYaml] = useState('');
  const [mergedYaml, setMergedYaml] = useState('');

  const disableControls = connectionStatus !== 'connected';
  const isDirty = visualDirty;

  const loadConfig = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await configFileApi.fetchConfigYaml();
      setDiffModalOpen(false);
      setServerYaml(data);
      setMergedYaml(data);
      loadVisualValuesFromYaml(data);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('notification.refresh_failed');
      setError(message);
    } finally {
      setLoading(false);
    }
  }, [loadVisualValuesFromYaml, t]);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  const getStatusText = () => {
    if (disableControls) return t('config_management.status_disconnected');
    if (loading) return t('config_management.status_loading');
    if (error) return t('config_management.status_load_failed');
    if (visualParseError) return t('config_management.visual_mode_unavailable');
    if (saving) return t('config_management.status_saving');
    if (isDirty) return t('config_management.status_dirty');
    return t('config_management.status_loaded');
  };

  const getStatusClass = () => {
    if (error || visualParseError) return styles.error;
    if (isDirty) return styles.modified;
    if (!loading && !saving) return styles.saved;
    return '';
  };

  const handleConfirmSave = async () => {
    setSaving(true);
    try {
      await configFileApi.saveConfigYaml(mergedYaml);
      const latestContent = await configFileApi.fetchConfigYaml();
      setDiffModalOpen(false);
      setServerYaml(latestContent);
      setMergedYaml(latestContent);
      loadVisualValuesFromYaml(latestContent);

      try {
        useConfigStore.getState().clearCache();
        await useConfigStore.getState().fetchConfig(undefined, true);
      } catch (refreshError: unknown) {
        const message =
          refreshError instanceof Error
            ? refreshError.message
            : typeof refreshError === 'string'
              ? refreshError
              : '';
        showNotification(
          `${t('notification.refresh_failed')}${message ? `: ${message}` : ''}`,
          'error'
        );
      }

      showNotification(t('config_management.save_success'), 'success');
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.save_failed')}: ${message}`, 'error');
    } finally {
      setSaving(false);
    }
  };

  const handleSave = async () => {
    if (visualParseError) {
      showNotification(t('config_management.visual_mode_save_blocked'), 'error');
      return;
    }

    setSaving(true);
    try {
      const latestServerYaml = await configFileApi.fetchConfigYaml();
      const latestDocument = parseDocument(latestServerYaml);
      if (latestDocument.errors.length > 0) {
        showNotification(
          t('config_management.visual_mode_latest_yaml_invalid', {
            message:
              latestDocument.errors[0]?.message ?? t('config_management.visual_mode_save_blocked'),
          }),
          'error'
        );
        return;
      }

      const nextMergedYaml = applyAuthChangesToYaml(latestServerYaml);
      const diffOriginal = latestDocument.toString({ indent: 2, lineWidth: 120, minContentWidth: 0 });

      if (diffOriginal === nextMergedYaml) {
        setServerYaml(latestServerYaml);
        setMergedYaml(nextMergedYaml);
        loadVisualValuesFromYaml(latestServerYaml);
        showNotification(t('config_management.diff.no_changes'), 'info');
        return;
      }

      setServerYaml(diffOriginal);
      setMergedYaml(nextMergedYaml);
      setDiffModalOpen(true);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.save_failed')}: ${message}`, 'error');
    } finally {
      setSaving(false);
    }
  };

  const handleReload = useCallback(() => {
    if (!isDirty) {
      void loadConfig();
      return;
    }

    showConfirmation({
      title: t('common.unsaved_changes_title'),
      message: t('config_management.reload_confirm_message'),
      confirmText: t('config_management.reload'),
      cancelText: t('common.cancel'),
      variant: 'danger',
      onConfirm: async () => {
        await loadConfig();
      },
    });
  }, [isDirty, loadConfig, showConfirmation, t]);

  return (
    <div className={styles.container}>
      <div className={styles.pageHeader}>
        <div className={styles.pageHeaderCopy}>
          <span className={styles.pageEyebrow}>{t('api_keys_management.eyebrow')}</span>
          <h1 className={styles.pageTitle}>{t('api_keys_management.title')}</h1>
          <p className={styles.description}>{t('api_keys_management.description')}</p>
        </div>

        <div className={styles.pageMeta}>
          <div className={`${styles.statusBadge} ${getStatusClass()}`}>{getStatusText()}</div>
          <div className={styles.tabBar}>
            <Button variant="secondary" size="sm" onClick={handleReload} disabled={loading || saving}>
              <IconRefreshCw size={16} />
              {t('config_management.reload')}
            </Button>
            <Button
              size="sm"
              onClick={handleSave}
              disabled={disableControls || loading || saving || !isDirty || diffModalOpen || !!visualParseError}
            >
              <IconCheck size={16} />
              {t('config_management.save')}
            </Button>
          </div>
        </div>
      </div>

      <div className={styles.workspaceShell}>
        <div className={styles.content}>
          {error ? <div className="error-box">{error}</div> : null}
          {!error && visualParseError ? (
            <div className="error-box">
              {t('config_management.visual_mode_unavailable_detail', { message: visualParseError })}
            </div>
          ) : null}

          <ConfigSection
            id="api-keys-management"
            indexLabel="01"
            icon={<IconKey size={16} />}
            title={t('api_keys_management.title')}
            description={t('api_keys_management.section_description')}
          >
            <div className="stack">
              <Input
                label={t('config_management.visual.sections.auth.auth_dir')}
                placeholder="~/.cli-proxy-api"
                value={visualValues.authDir}
                onChange={(e) => setVisualValues({ authDir: e.target.value })}
                disabled={disableControls || loading}
                hint={t('config_management.visual.sections.auth.auth_dir_hint')}
              />
              <ApiKeysCardEditor
                value={visualValues.apiKeys}
                disabled={disableControls || loading}
                onChange={(apiKeys) => setVisualValues({ apiKeys })}
              />
            </div>
          </ConfigSection>
        </div>
      </div>

      <DiffModal
        open={diffModalOpen}
        original={serverYaml}
        modified={mergedYaml}
        onConfirm={handleConfirmSave}
        onCancel={() => setDiffModalOpen(false)}
        loading={saving}
      />
    </div>
  );
}
