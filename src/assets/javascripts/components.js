'use strict';

Vue.component('setting-field', {
  props: ['label', 'value', 'href', 'draft', 'placeholder'],
  template: `
    <div class="setting-field">
      <div class="settings-dialog-field">
        <a v-if="href" class="settings-dialog-field-label" :href="href" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">{{ label }}</a>
        <span v-else class="settings-dialog-field-label">{{ label }}</span>
        <button v-if="!draft.editing" type="button" class="settings-dialog-field-value-action" :aria-label="'编辑' + label" @click="edit">{{ value || '未设置' }}</button>
        <div v-else class="setting-field-editor">
          <input class="form-control" type="text" :aria-label="label" v-model="draft.value" :disabled="draft.saving" :placeholder="placeholder" @keydown.enter.stop.prevent="$emit('save')" @keydown.esc.stop.prevent="cancel" v-focus>
          <div class="setting-field-actions">
            <button type="button" class="settings-btn" :disabled="draft.saving" @click="cancel">取消</button>
            <button type="button" class="settings-btn settings-btn-primary" :disabled="draft.saving" @click="$emit('save')">{{ draft.saving ? '保存中' : '保存' }}</button>
          </div>
          <p v-if="draft.error" class="text-danger setting-field-error" role="alert">{{ draft.error }}</p>
        </div>
      </div>
    </div>
  `,
  methods: {
    edit: function() {
      this.draft.value = this.value || ''
      this.draft.editing = true
      this.draft.error = ''
    },
    cancel: function() {
      if (this.draft.saving) return
      this.draft.value = this.value || ''
      this.draft.editing = false
      this.draft.error = ''
    },
  },
})

var COLLAPSE_DURATION = 200

function prefersReducedMotion() {
  return window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

// Vertical box properties animated together with height so padding, margins
// and borders shrink smoothly instead of snapping at the start/end.
var COLLAPSE_PROPS = ['height', 'paddingTop', 'paddingBottom', 'marginTop', 'marginBottom', 'borderTopWidth', 'borderBottomWidth']
var COLLAPSE_CSS = ['height', 'padding-top', 'padding-bottom', 'margin-top', 'margin-bottom', 'border-top-width', 'border-bottom-width']

// Animate an element between collapsed (all vertical box props 0) and its
// natural size. `done` runs once the transition settles (or immediately when
// motion is reduced).
function animateHeight(el, open, done) {
  clearTimeout(el._collapseTimer)
  if (el._collapseFinish) el._collapseFinish(true)
  if (prefersReducedMotion()) return done && done()

  var cs = getComputedStyle(el)
  var full = {}
  COLLAPSE_PROPS.forEach(function(prop) { full[prop] = cs[prop] })
  full.height = el.scrollHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom) + 'px'
  if (cs.boxSizing === 'border-box') {
    full.height = el.getBoundingClientRect().height + 'px'
  }
  var apply = function(state) {
    COLLAPSE_PROPS.forEach(function(prop) { el.style[prop] = state === 'full' ? full[prop] : '0px' })
    el.style.opacity = state === 'full' ? '1' : '0'
  }

  el.style.overflow = 'hidden'
  el.style.transition = 'none'
  apply(open ? 'zero' : 'full')
  el.offsetHeight  // flush so the start values apply before transitioning
  el.style.transition = COLLAPSE_CSS.concat('opacity').map(function(p) { return p + ' ' + COLLAPSE_DURATION + 'ms ease' }).join(', ')
  apply(open ? 'full' : 'zero')

  var finish = function(interrupted) {
    el.removeEventListener('transitionend', onEnd)
    clearTimeout(el._collapseTimer)
    el._collapseFinish = null
    // when closing, let `done` hide the element before clearing inline sizes,
    // otherwise it flashes back to full height for a frame
    if (!open && !interrupted && done) done()
    el.style.transition = el.style.opacity = el.style.overflow = ''
    COLLAPSE_PROPS.forEach(function(prop) { el.style[prop] = '' })
    if ((open || interrupted) && done) done()
  }
  var onEnd = function(e) { if (e.target === el && e.propertyName === 'height') finish(false) }
  el._collapseFinish = finish
  el.addEventListener('transitionend', onEnd)
  el._collapseTimer = setTimeout(function() { finish(false) }, COLLAPSE_DURATION + 50)
}

// <collapse> wraps a single v-if / v-show child and animates its height.
Vue.component('collapse', {
  functional: true,
  render: function(h, ctx) {
    return h('transition', {
      props: {css: false},
      on: {
        enter: function(el, done) { animateHeight(el, true, done) },
        leave: function(el, done) { animateHeight(el, false, done) },
      },
    }, ctx.children)
  },
})

Vue.component('drag', {
  props: ['width'],
  template: '<div class="drag"></div>',
  mounted: function() {
    var self = this
    var startX = undefined
    var initW = undefined
    var onMouseMove = function(e) {
      var offset = e.clientX - startX
      var newWidth = initW + offset
      self.$emit('resize', newWidth)
    }
    var onMouseUp = function(e) {
      document.removeEventListener('mousemove', onMouseMove)
      document.removeEventListener('mouseup', onMouseUp)
    }
    this.$el.addEventListener('mousedown', function(e) {
      startX = e.clientX
      initW = self.width
      document.addEventListener('mousemove', onMouseMove)
      document.addEventListener('mouseup', onMouseUp)
    })
  },
})

Vue.component('modal', {
  props: ['open'],
  template: `
    <div class="modal custom-modal" tabindex="-1" role="dialog" aria-modal="true" aria-label="确认操作" v-if="$props.open" @keydown="handleKey">
      <div class="modal-dialog">
        <div class="modal-content" ref="content">
          <div class="modal-body">
            <slot v-if="$props.open"></slot>
          </div>
        </div>
      </div>
    </div>
  `,
  data: function() {
    return {opening: false, returnFocus: null}
  },
  beforeDestroy: function() {
    document.removeEventListener('click', this.handleClick)
  },
  watch: {
    'open': function(newVal) {
      if (newVal) {
        this.opening = true
        this.returnFocus = document.activeElement
        document.addEventListener('click', this.handleClick)
        this.$nextTick(function() {
          var button = this.$el.querySelector('button')
          if (button) button.focus()
        })
      } else {
        document.removeEventListener('click', this.handleClick)
        this.$nextTick(function() {
          if (this.returnFocus && this.returnFocus.isConnected) this.returnFocus.focus({preventScroll: true})
        })
      }
    },
  },
  methods: {
    handleKey: function(event) {
      event.stopPropagation()
      if (event.key === 'Escape') {
        event.preventDefault()
        this.$emit('hide')
      }
      if (event.key !== 'Tab') return
      var buttons = this.$el.querySelectorAll('button:not(:disabled)')
      var first = buttons[0], last = buttons[buttons.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    },
    handleClick: function(e) {
      if (this.opening) {
        this.opening = false
        return
      }
      if (e.target.closest('.custom-modal') !== this.$el) return
      if (e.target.closest('.modal-content') == null) this.$emit('hide')
    },
  },
})

function dateRepr(d, maxDays) {
  var sec = (new Date().getTime() - d.getTime()) / 1000
  var neg = sec < 0
  var out = ''

  sec = Math.abs(sec)
  if (sec < 2700)  // less than 45 minutes
    out = Math.round(sec / 60) + 'm'
  else if (sec < 86400)  // less than 24 hours
    out = Math.round(sec / 3600) + 'h'
  else if (maxDays)
    out = Math.min(Math.round(sec / 86400), maxDays) + 'd'
  else if (sec < 604800)  // less than a week
    out = Math.round(sec / 86400) + 'd'
  else
    out = d.toLocaleDateString(undefined, {year: "numeric", month: "long", day: "numeric"})

  if (neg) return '-' + out
  return out
}

Vue.component('relative-time', {
  props: ['val', 'maxDays'],
  data: function() {
    var d = new Date(this.val)
    return {
      'date': d,
      'formatted': dateRepr(d, this.maxDays),
      'interval': null,
    }
  },
  template: '<time :datetime="val">{{ formatted }}</time>',
  mounted: function() {
    this.interval = setInterval(function() {
      this.formatted = dateRepr(this.date, this.maxDays)
    }.bind(this), 600000)  // every 10 minutes
  },
  destroyed: function() {
    clearInterval(this.interval)
  },
})
