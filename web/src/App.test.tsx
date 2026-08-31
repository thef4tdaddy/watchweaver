import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import App from './App'

describe('App', () => {
  it('renders the WatchWeaver scaffold heading', () => {
    render(<App />)

    expect(
      screen.getByRole('heading', { level: 1, name: 'WatchWeaver' }),
    ).toBeInTheDocument()
    expect(screen.getByText(/frontend scaffold is running/i)).toBeInTheDocument()
  })
})
