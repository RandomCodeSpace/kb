// "Fix login !high #auth #type::bug @fri ~M" -> {title, prio, tags, due, effort}
import { PRIO_WORDS } from './filters';
import { dueDays, isoDate, localToday } from './format';
import { isProjectTag, withLabel } from './labels';

const WEEKDAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
export function parseQuickAdd(text) {
  const out = { title: [], prio: null, tags: [], due: null, effort: null };
  for (const w of text.split(/\s+/)) {
    if (!w) continue;
    let m;
    if ((m = /^!(high|medium|med|low|[123])$/i.exec(w))) { out.prio = PRIO_WORDS[m[1].toLowerCase()]; continue; }
    if ((m = /^#([\w:.-]+)$/.exec(w)) && !/^\d+$/.test(m[1]) && !isProjectTag(m[1])) { out.tags = withLabel(out.tags, m[1]); continue; }
    if ((m = /^~([sml])$/i.exec(w))) { out.effort = m[1].toUpperCase(); continue; }
    if ((m = /^@(\d{4}-\d{2}-\d{2}|today|tomorrow|sun|mon|tue|wed|thu|fri|sat)$/i.exec(w))) {
      const v = m[1].toLowerCase();
      const today = localToday();
      let d = null;
      if (v === 'today') d = today;
      else if (v === 'tomorrow') d = new Date(today.getTime() + 86400000);
      else if (WEEKDAYS.includes(v)) d = new Date(today.getTime() + (((WEEKDAYS.indexOf(v) - today.getDay() + 7) % 7) || 7) * 86400000);
      else if (dueDays(v) !== null) out.due = v;
      if (d) out.due = isoDate(d);
      if (out.due) continue;
    }
    out.title.push(w);
  }
  out.title = out.title.join(' ');
  return out;
}
