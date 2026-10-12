# Catalogues

One file a language, named by its tag in lower case: `de.json`, `pt-br.json`.
Each is a JSON object from the English the application says to what it says
instead:

```json
{
  "Save as Job…": "Als Auftrag speichern …",
  "%d rows into %s": "%d Zeilen in %s"
}
```

- The English is the key, exactly as `messages.json` beside this directory
  lists it. That file is every sentence there is to translate, and is kept in
  step with the code by a test.
- A sentence left out, or left as `""`, is said in English.
- A translation must ask for the same values as its English, in the same
  order: `%d` then `%s` stays `%d` then `%s`, wherever in the sentence they
  go. One that does not is not used, and is reported when the application
  starts.
- There is no catalogue for English, and none yet for anything else.
