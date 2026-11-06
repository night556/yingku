const labels = {intro: '片头', outro: '片尾'};
const seasonKeys = {intro: 'intro', outro: 'outroFromEnd'};
const autoSkipKey = 'yingku_auto_skip';
let autoSkip = false;

export const skipEditorMarkup = shareable => `
  <details class="player-markers">
    <summary>标记片头片尾</summary>
    <label class="marker-kind">片段
      <select data-marker-kind><option value="intro">片头</option><option value="outro">片尾</option></select>
    </label>
    ${shareable ? `<label class="marker-kind">应用范围
      <select data-marker-scope><option value="video">仅本集</option><option value="season">本季默认</option></select>
    </label>` : ''}
    <p class="marker-help marker-origin"></p>
    <div class="marker-point">
      <label for="marker-start">起点</label>
      <input id="marker-start" type="text" inputmode="decimal" placeholder="0:00" autocomplete="off">
      <button class="button quiet" type="button" data-marker-current="start" aria-label="把起点设为当前时间">用当前</button>
    </div>
    <div class="marker-point">
      <label for="marker-end">终点</label>
      <input id="marker-end" type="text" inputmode="decimal" placeholder="1:20" autocomplete="off">
      <button class="button quiet" type="button" data-marker-current="end" aria-label="把终点设为当前时间">用当前</button>
    </div>
    <p class="marker-help">拖动进度条定位后选「用当前」，也可以填写秒数或时间，例如 80 或 1:20。</p>
    <div class="marker-actions">
      <button class="button primary" type="button" data-marker-save>保存标记</button>
      <button class="button quiet" type="button" data-marker-clear>清除标记</button>
    </div>
    ${shareable ? '<button class="button quiet marker-disable" type="button" data-marker-disable hidden></button>' : ''}
    <p class="marker-status" role="status"></p>
  </details>`;

function formatMarkerTime(value) {
  const millis = Math.round(value * 1000);
  const seconds = Math.floor(millis / 1000);
  const minutes = Math.floor(seconds / 60);
  const fraction = String(millis % 1000).padStart(3, '0').replace(/0+$/, '');
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}${fraction ? '.' + fraction : ''}`;
}

function parseMarkerTime(input) {
  const value = input.trim();
  if (!/^(?:\d+:){0,2}\d+(?:\.\d+)?$/.test(value)) return NaN;
  const parts = value.split(':').map(Number);
  if (parts.slice(1).some(number => number >= 60)) return NaN;
  return parts.reduce((total, number) => total * 60 + number, 0);
}

export function mountSkipSegments({root, video, signal, segments, seasonSegments, saveSegment, canAutoSkip, onSeek}) {
  const editor = root.querySelector('.player-markers');
  const skip = root.querySelector('.player-skip');
  const auto = root.querySelector('#player-auto-skip');
  const selector = editor.querySelector('[data-marker-kind]');
  const scopeSelector = editor.querySelector('[data-marker-scope]');
  const start = editor.querySelector('#marker-start');
  const end = editor.querySelector('#marker-end');
  const save = editor.querySelector('[data-marker-save]');
  const clear = editor.querySelector('[data-marker-clear]');
  const disable = editor.querySelector('[data-marker-disable]');
  const currentButtons = [...editor.querySelectorAll('[data-marker-current]')];
  const origin = editor.querySelector('.marker-origin');
  const status = editor.querySelector('.marker-status');
  const marks = {
    intro: {segment: segments.intro, disabled: segments.introSkipDisabled},
    outro: {segment: segments.outro, disabled: segments.outroSkipDisabled},
  };
  const shared = {...seasonSegments};
  const drafts = {};
  let kind = selector.value, scope = 'video', saving = false;

  const listen = (element, event, callback) => element.addEventListener(event, callback, {signal});
  const durationKnown = () => Number.isFinite(video.duration) && video.duration > 0;
  const fitsVideo = mark => durationKnown() && mark && mark.start >= 0 && mark.start < mark.end && mark.end <= video.duration;
  const draftKey = () => `${scope}-${kind}`;
  function seasonMark(name) {
    if (name === 'intro') return shared.intro;
    const offset = shared.outroFromEnd;
    if (offset && durationKnown()) return {start: video.duration - offset.end, end: video.duration - offset.start};
    return null;
  }
  function playbackMarks() {
    const resolve = name => {
      const own = marks[name];
      const mark = own.segment || seasonMark(name);
      return !own.disabled && fitsVideo(mark) ? mark : null;
    };
    const intro = resolve('intro'), outro = resolve('outro');
    if (intro && outro && intro.end > outro.start) {
      // A different cut can make inherited timings overlap. Keep a specific override.
      return {intro: marks.intro.segment ? intro : null, outro: marks.outro.segment ? outro : null};
    }
    return {intro, outro};
  }
  function draftFor() {
    const inherited = seasonMark(kind);
    const fallback = fitsVideo(inherited) ? inherited : null;
    const own = marks[kind];
    const mark = scope === 'season'
      ? (shared[seasonKeys[kind]] ? fallback : own.segment)
      : own.segment || (!own.disabled && fallback);
    if (mark) return {start: formatMarkerTime(mark.start), end: formatMarkerTime(mark.end)};
    return {start: kind === 'intro' ? '0:00' : '', end: kind === 'outro' && durationKnown() ? formatMarkerTime(video.duration) : ''};
  }
  function setStatus(message, error = false) {
    status.textContent = message;
    status.classList.toggle('marker-error', error);
  }
  function updateButtons() {
    selector.disabled = start.disabled = end.disabled = saving;
    if (scopeSelector) scopeSelector.disabled = saving;
    const own = marks[kind];
    const hasDefault = Boolean(shared[seasonKeys[kind]]);
    save.disabled = saving || !durationKnown();
    clear.disabled = saving || !(scope === 'season' ? hasDefault : own.segment || own.disabled);
    save.textContent = scope === 'season' ? '保存本季默认' : seasonSegments ? '保存本集标记' : '保存标记';
    clear.textContent = scope === 'video' && (hasDefault || own.disabled) ? '恢复跟随本季' : '清除标记';
    if (disable) {
      disable.hidden = scope !== 'video' || !hasDefault || own.disabled;
      disable.disabled = saving;
      disable.textContent = '本集不跳过' + labels[kind];
    }
    for (const button of currentButtons) button.disabled = saving || !durationKnown();
  }
  function showDraft() {
    const draft = drafts[draftKey()] || draftFor();
    start.value = draft.start;
    end.value = draft.end;
    const own = marks[kind];
    if (scope === 'season') {
      origin.textContent = kind === 'intro' ? '同季沿用开场后的时间。' : '同季沿用距视频结束的时间。';
    } else if (own.disabled) {
      origin.textContent = '本集不跳过' + labels[kind] + '。';
    } else if (seasonSegments && own.segment) {
      origin.textContent = '本集使用单独标记。';
    } else if (shared[seasonKeys[kind]]) {
      origin.textContent = playbackMarks()[kind] ? '本集沿用本季默认标记。' : '本季默认标记与本集时长或其他标记不匹配。';
    } else {
      origin.textContent = '';
    }
    updateButtons();
  }
  function activeSegment() {
    if (!durationKnown() || video.ended || video.error) return null;
    for (const [name, mark] of Object.entries(playbackMarks())) {
      if (mark && video.currentTime >= mark.start && video.currentTime < mark.end) return {name, end: mark.end};
    }
    return null;
  }
  function updateSkip() {
    const active = activeSegment();
    skip.hidden = !active;
    if (active) skip.textContent = '跳过' + labels[active.name];
    return active;
  }
  function jump(endTime) {
    video.currentTime = endTime;
    updateSkip();
    onSeek();
  }
  function checkPlayback() {
    const active = updateSkip();
    if (autoSkip && active && !video.paused && !video.seeking && canAutoSkip() && active.end - video.currentTime > 0.001) {
      jump(active.end);
    }
  }
  async function persist(input) {
    if (saving) return;
    saving = true;
    updateButtons();
    setStatus('正在保存…');
    try {
      const result = await saveSegment(kind, scope, input);
      if (signal.aborted) return;
      if (scope === 'season') shared[seasonKeys[kind]] = result.segment;
      else marks[kind] = result;
      delete drafts[draftKey()];
      showDraft();
      updateSkip();
      setStatus(input?.disabled ? '本集已关闭该片段的跳过' : input ? labels[kind] + '标记已保存' : '标记设置已清除');
    } catch (error) {
      if (!signal.aborted) setStatus(error.message || '标记保存失败', true);
    } finally {
      saving = false;
      if (!signal.aborted) updateButtons();
    }
  }

  function changeEditor() {
    kind = selector.value;
    scope = scopeSelector?.value || 'video';
    setStatus('');
    showDraft();
  }
  function rememberDraft() {
    drafts[draftKey()] = {start: start.value, end: end.value};
    setStatus('');
  }
  listen(selector, 'change', changeEditor);
  if (scopeSelector) listen(scopeSelector, 'change', changeEditor);
  listen(start, 'input', rememberDraft);
  listen(end, 'input', rememberDraft);
  for (const button of currentButtons) {
    listen(button, 'click', () => {
      const input = button.dataset.markerCurrent === 'start' ? start : end;
      input.value = formatMarkerTime(video.currentTime);
      rememberDraft();
    });
  }
  listen(save, 'click', () => {
    const from = parseMarkerTime(start.value);
    let to = parseMarkerTime(end.value);
    if (durationKnown() && Math.abs(to - video.duration) <= 0.0005) to = video.duration;
    if (!durationKnown() || !(from >= 0 && from < to && to <= video.duration)) {
      setStatus('请填写有效的起点和终点，终点不能超过视频时长。', true);
      return;
    }
    void persist({start: from, end: to, duration: video.duration});
  });
  listen(clear, 'click', () => void persist(null));
  if (disable) listen(disable, 'click', () => void persist({disabled: true}));
  listen(skip, 'click', () => {
    const active = activeSegment();
    if (!active) return;
    jump(active.end);
    root.querySelector('.player-stage').focus({preventScroll: true});
  });
  try { autoSkip = localStorage.getItem(autoSkipKey) === '1'; } catch {}
  auto.checked = autoSkip;
  listen(auto, 'change', () => {
    autoSkip = auto.checked;
    try { localStorage.setItem(autoSkipKey, autoSkip ? '1' : '0'); } catch {}
  });
  listen(video, 'durationchange', () => {
    showDraft();
    updateSkip();
  });
  for (const event of ['playing', 'timeupdate', 'seeked']) listen(video, event, checkPlayback);
  for (const event of ['ended', 'error', 'emptied']) listen(video, event, updateSkip);
  showDraft();
  updateSkip();
}
