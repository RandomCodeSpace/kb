import { useId } from 'react';
import { Children, cloneElement, isValidElement } from 'react';
import { cn } from '@/lib/utils';

// shadcn/ui Field: label over control over message, with the message wired
// up as the control's description. An error tone also marks it invalid.
function Field({ className, label, hint, action, message, tone, htmlFor, children, ...props }) {
  const auto = useId();
  const id = htmlFor || auto;
  const msgId = id + '-msg';
  const control = Children.map(children, (child) => (isValidElement(child) && !child.props.id
    ? cloneElement(child, { id, 'aria-describedby': msgId, 'aria-invalid': tone === 'error' ? 'true' : child.props['aria-invalid'] })
    : child));
  const head = (
    <label className="label-11" htmlFor={id}>
      {label}
      {hint && <> <span className="ml-1.5 font-normal tracking-normal text-fg-3">{hint}</span></>}
    </label>
  );
  return (
    <div data-slot="field" className={cn('field', className)} {...props}>
      {action ? <div className="field-head">{head}{action}</div> : head}
      {control}
      <FieldMessage id={msgId} tone={tone}>{message}</FieldMessage>
    </div>
  );
}

// The line under a control: ok, warn and error tones. Empty messages collapse.
function FieldMessage({ className, tone, mono, children, ...props }) {
  return <p data-slot="field-message" className={cn('field-msg', tone && `is-${tone}`, mono && 'num', className)} {...props}>{children}</p>;
}

// The small caps label used above ad-hoc groups.
function FieldLabel({ className, as: Comp = 'span', ...props }) {
  return <Comp data-slot="field-label" className={cn('label-11', className)} {...props} />;
}

export { Field, FieldMessage, FieldLabel };
