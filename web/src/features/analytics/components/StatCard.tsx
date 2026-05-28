import styles from '../Analytics.module.scss';

export function StatCard(props: {
  label: string;
  value: string;
  sublabel?: string;
  accent: 'primary' | 'success' | 'error';
}) {
  return (
    <div className={`${styles.statCard} ${styles[`accent_${props.accent}`]}`}>
      <span className={styles.statLabel}>{props.label}</span>
      <span className={styles.statValue}>{props.value}</span>
      {props.sublabel && <span className={styles.statSub}>{props.sublabel}</span>}
    </div>
  );
}
