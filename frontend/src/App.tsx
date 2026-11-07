import { useState, type FormEvent } from 'react'
import {GreetService} from "../bindings/github.com/anujkaushik1/SpyDB";

function App() {
  const [name, setName] = useState<string>('');
  const [greeting, setGreeting] = useState<string>('');

  const doGreet = (e: FormEvent) => {
    e.preventDefault();
    GreetService.Greet(name.trim()).then(setGreeting).catch(console.error);
  };

  return (
    <main className="container">
      <form className="greet" onSubmit={doGreet}>
        <input
          aria-label="name"
          className="input"
          value={name}
          onChange={(e) => setName(e.target.value)}
          type="text"
          placeholder="Enter your name"
          autoComplete="off"
          autoFocus
        />
        <button className="btn" type="submit" disabled={!name.trim()}>Greet</button>
      </form>
      <p className="result" aria-live="polite">{greeting}</p>
    </main>
  )
}

export default App
