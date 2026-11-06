export function createSourceMigration({form, button, source, api, esc}) {
  const panel = document.createElement('section');
  panel.className = 'source-move';
  panel.hidden = true;
  panel.innerHTML = `
    <p class="section-note">改路径后先检查旧视频的新位置，再确认迁移。作品资料和观看进度会保留。</p>
    <div class="source-move-rules"></div>
    <button class="button small quiet" type="button" data-move-add>添加子目录对应关系</button>
    <div class="source-move-results" aria-live="polite"></div>`;
  form.querySelector('#source-fields').after(panel);

  const results = panel.querySelector('.source-move-results');
  const rules = panel.querySelector('.source-move-rules');
  const overrides = new Map();
  let token = '', revision = 0;
  const rootChanged = () => form.elements.root.value.trim() !== source.root;

  function resetPreview() {
    revision++;
    token = '';
    panel.hidden = !rootChanged();
    button.textContent = rootChanged() ? '检查目录迁移' : '保存并连接';
    const summary = results.querySelector('[data-move-summary]');
    if (summary) summary.textContent = '路径有改动，请重新检查。';
  }

  form.addEventListener('input', event => {
    if (event.target.matches('[data-move-file]')) {
      overrides.set(event.target.dataset.moveFile, event.target.value.trim());
    }
    resetPreview();
  });

  panel.addEventListener('click', event => {
    if (event.target.closest('[data-move-add]')) {
      rules.insertAdjacentHTML('beforeend', `
        <div class="source-move-rule">
          <label class="field"><span>原子目录</span><input data-move-from placeholder="例如 美剧"></label>
          <label class="field"><span>新子目录</span><input data-move-to placeholder="例如 TV，留空表示新根目录"></label>
          <button class="button small quiet" type="button" data-move-remove>移除</button>
        </div>`);
      rules.lastElementChild.querySelector('input').focus();
      resetPreview();
    } else if (event.target.closest('[data-move-remove]')) {
      event.target.closest('.source-move-rule').remove();
      resetPreview();
    }
  });

  function render(result) {
    const issues = result.rows.filter(row => row.problem);
    const ready = result.rows.filter(row => !row.problem);
    results.innerHTML = `
      <div class="notice ${issues.length || result.blocked ? '' : 'neutral'}">
        <div><strong data-move-summary>${ready.length} 个视频已找到${issues.length ? `，${issues.length} 个需要处理` : ''}</strong>
        <p class="source-move-root">${esc(result.oldRoot)} → ${esc(result.newRoot)}</p></div>
      </div>
      ${result.blocked ? `<div class="error-box">${esc(result.blocked)}</div>` : ''}
      ${issues.length ? `<p class="section-note">子目录改名可在上面填写对应关系。个别视频改名时，填写它在新根目录下的相对路径。</p>
        <div class="source-move-list">${issues.map(row => `<div class="source-move-row">
          <strong>${esc(row.old)}</strong><p>${esc(row.new.replace(/^\//, ''))} · ${esc(row.problem)}</p>
          <label class="field"><span>指定新文件（相对路径）</span><input data-move-file="${esc(row.id)}" value="${esc(overrides.get(row.id) || '')}" placeholder="例如 TV/某剧/第01集.mp4"></label>
        </div>`).join('')}</div>` : ''}
      ${ready.length ? `<details><summary>查看 ${ready.length} 个已找到的视频</summary>
        <div class="source-move-list">${ready.map(row => `<div class="source-move-row">${esc(row.old)} → ${esc(row.new.replace(/^\//, ''))}</div>`).join('')}</div>
      </details>` : ''}`;
    button.textContent = token ? '确认迁移' : '重新检查';
  }

  return {
    rootChanged,
    async submit(data) {
      const base = `/sources/${encodeURIComponent(source.id)}/migration`;
      if (token) {
        const fields = [...form.querySelectorAll('input:not([disabled]), button:not([disabled])')];
        fields.forEach(field => { field.disabled = true; });
        try {
          return await api(base + '/commit', {token});
        } catch (error) {
          resetPreview();
          throw error;
        } finally {
          fields.forEach(field => { field.disabled = false; });
        }
      }
      const version = revision;
      const directories = [...rules.children].map(row => ({
        from: row.querySelector('[data-move-from]').value.trim(),
        to: row.querySelector('[data-move-to]').value.trim(),
      })).filter(rule => rule.from || rule.to);
      const files = Object.fromEntries([...overrides].filter(([, value]) => value));
      const result = await api(base + '/preview', {root: data.root, name: data.name, directories, files});
      if (!form.isConnected || revision !== version) return null;
      token = result.token;
      render(result);
      if (token && result.rows.length === 0) return this.submit(data);
      return null;
    },
  };
}
