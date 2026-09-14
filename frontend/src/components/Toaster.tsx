import { useToastStore } from '../stores/toast'
import { scopedClasses } from '../utils/scoped-classes'
import styles from './Toaster.module.css'

export default function Toaster() {
  const toast = useToastStore()
  const css = (...values: Array<string | false | null | undefined>) => scopedClasses(styles, ...values)

  return (
    <div className={css('toast-wrap')} aria-live="polite" aria-relevant="additions removals">
      {toast.toasts.map((item) => (
        <div key={item.id} className={css('toast', item.type)}>
          <div className={css('toast-msg')}>{item.message}</div>
          <button className={css('toast-x')} type="button" aria-label="关闭" onClick={() => toast.remove(item.id)}>×</button>
        </div>
      ))}
    </div>
  )
}
