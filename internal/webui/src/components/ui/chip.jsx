import { cn } from '@/lib/utils';
import { Icon } from '@/components/icon';
import { splitLabel, labelHue } from '@/lib/labels';

// shadcn/ui Badge as kb's 20px chip. A `tag` draws a label: dot plus name, or
// the two-tone scoped pill. `tone` and `hue` colour the chip from the theme
// through data attributes, so no inline styles are needed.
function Chip({ className, tag, short, tone, hue, dot, mono, icon, remove, removeLabel, onClick, pressed, title, enter, children, ...props }) {
  let body = children;
  let kind = '';
  const data = {};
  if (tag) {
    const { scope, value } = splitLabel(tag);
    const isShort = short && scope;
    kind = scope && !isShort ? 'chip-scoped' : 'chip-dot';
    data['data-tag'] = tag;
    data['data-h'] = labelHue(tag);
    body = isShort ? <span>{value}</span> : scope ? <><span className="k">{scope}</span><span className="v">{value}</span></> : <span>{tag}</span>;
  } else if (dot) kind = 'chip-dot';
  else if (tone !== undefined) kind = 'chip-tone';
  if (tone) data['data-tone'] = tone;
  if (hue) data['data-hue'] = hue;
  const classes = cn('chip', kind, mono && 'chip-mono', enter && 'enter', className);
  const content = (
    <>
      {icon && <Icon name={icon} size={11} />}
      {body}
      {remove && (
        <button type="button" className="x" aria-label={removeLabel || `Remove ${tag || ''}`.trim()} onClick={(e) => { e.stopPropagation(); remove(); }}>
          <Icon name="x" size={10} />
        </button>
      )}
    </>
  );
  if (onClick) {
    return (
      <button type="button" data-slot="chip" className={classes} data-tip={title || tag} aria-pressed={pressed ? 'true' : 'false'} onClick={(e) => { e.stopPropagation(); onClick(e); }} {...data} {...props}>
        {content}
      </button>
    );
  }
  return <span data-slot="chip" className={classes} data-tip={title || tag} {...data} {...props}>{content}</span>;
}

export { Chip };
