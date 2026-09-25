import { useEffect, useState } from 'react'

/** Fraction of the page scrolled (0..1) and whether we're past the top. */
export function useScroll() {
  const [state, setState] = useState({ progress: 0, scrolled: false })
  useEffect(() => {
    let frame = 0
    const update = () => {
      frame = 0
      const max = document.documentElement.scrollHeight - window.innerHeight
      setState({ progress: max > 0 ? window.scrollY / max : 0, scrolled: window.scrollY > 8 })
    }
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update)
    }
    update()
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll)
    return () => {
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
      if (frame) cancelAnimationFrame(frame)
    }
  }, [])
  return state
}
