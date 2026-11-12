// Row actions (rename, archive, restore, delete) are hover-revealed on a mouse.
// A touch screen has no hover, so there they are always shown and 44px square.
export const TOUCH_SIZE =
  'pointer-coarse:inline-flex pointer-coarse:items-center pointer-coarse:justify-center ' +
  'pointer-coarse:min-h-11 pointer-coarse:min-w-11'
export const ROW_BTN =
  `opacity-0 focus-visible:opacity-100 group-focus-within:opacity-100 group-hover:opacity-100 text-gray-300 pointer-coarse:opacity-100 ${TOUCH_SIZE}`
